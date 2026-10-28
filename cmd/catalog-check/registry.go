package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result of resolving one image reference anonymously.
type result struct {
	Status    string   `json:"status"` // ok missing private rate-limited error
	Archs     []string `json:"archs,omitempty"`
	Detail    string   `json:"detail,omitempty"`
	CheckedAt int64    `json:"checked_at"`
}

type ref struct {
	host, repo, tag, digest string
	tagSet                  bool // the reference names a tag (else "latest" is implied)
}

const hubRegistry = "registry-1.docker.io"

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?-([^}]*))?\}`)

// resolveVars replaces ${X:-default} / ${X-default} with the default. ok is false when a
// variable has no default (the image depends on an answer and cannot be checked).
func resolveVars(img string) (string, bool) {
	ok := true
	out := varRe.ReplaceAllStringFunc(img, func(m string) string {
		sub := varRe.FindStringSubmatch(m)
		if !strings.Contains(m, "-") {
			ok = false
		}
		return sub[2]
	})
	return out, ok && !strings.Contains(out, "$")
}

// parseRef splits an image reference like docker does (Docker Hub default, library/ prefix).
func parseRef(img string) (ref, bool) {
	r := ref{host: hubRegistry, tag: "latest"}
	name := img
	if at := strings.Index(name, "@"); at >= 0 {
		name, r.digest = name[:at], name[at+1:]
	}
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.host, name = first, rest
		if r.host == "docker.io" || r.host == "index.docker.io" {
			r.host = hubRegistry
		}
	}
	if c := strings.LastIndex(name, ":"); c > strings.LastIndex(name, "/") {
		name, r.tag, r.tagSet = name[:c], name[c+1:], true
	}
	if r.host == hubRegistry && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	r.host, r.repo = strings.ToLower(r.host), strings.ToLower(name)
	return r, name != "" && r.tag != ""
}

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json"

type checker struct {
	client  *http.Client
	hubAPI  string // https://hub.docker.com
	sleep   func(time.Duration)
	mu      sync.Mutex
	pause   map[string]time.Time // host -> rate limited until
	tokens  map[string]string    // host/repo -> bearer token
	retries int
}

func newChecker() *checker {
	return &checker{client: &http.Client{Timeout: 30 * time.Second}, hubAPI: "https://hub.docker.com",
		sleep: time.Sleep, pause: map[string]time.Time{}, tokens: map[string]string{}, retries: 4}
}

var errRateLimited = errors.New("rate limited")

// fetch does one request with retries: network errors and 5xx back off exponentially, 429 pauses
// every worker for that host (Retry-After, else 60 s) and resumes.
func (c *checker) fetch(ctx context.Context, method, u string, hdr map[string]string) (*http.Response, []byte, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return nil, nil, err
	}
	backoff, limited := time.Second, 0
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		until := c.pause[pu.Host]
		c.mu.Unlock()
		if d := time.Until(until); d > 0 {
			c.sleep(d)
		}
		req, _ := http.NewRequestWithContext(ctx, method, u, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := c.client.Do(req)
		var body []byte
		if err == nil {
			body, _ = io.ReadAll(io.LimitReader(res.Body, 4<<20))
			res.Body.Close()
		}
		switch {
		case err == nil && res.StatusCode == http.StatusTooManyRequests:
			if limited++; limited > 6 {
				return res, body, errRateLimited
			}
			wait := 60 * time.Second
			if s, e := strconv.Atoi(res.Header.Get("Retry-After")); e == nil && s > 0 {
				wait = min(time.Duration(s)*time.Second, 10*time.Minute)
			}
			c.mu.Lock()
			c.pause[pu.Host] = time.Now().Add(wait)
			c.mu.Unlock()
			continue
		case err == nil && res.StatusCode < 500:
			return res, body, nil
		}
		if attempt >= c.retries || ctx.Err() != nil {
			if err == nil {
				err = fmt.Errorf("%s answered %d", pu.Host, res.StatusCode)
			}
			return nil, nil, err
		}
		c.sleep(backoff)
		backoff *= 2
	}
}

var bearerRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// token gets an anonymous pull token from the realm in WWW-Authenticate (one per repo).
func (c *checker) token(ctx context.Context, r ref, challenge string) (string, error) {
	key := r.host + "/" + r.repo
	c.mu.Lock()
	t, ok := c.tokens[key]
	c.mu.Unlock()
	if ok {
		return t, nil
	}
	params := map[string]string{}
	for _, m := range bearerRe.FindAllStringSubmatch(challenge, -1) {
		params[strings.ToLower(m[1])] = m[2]
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Scheme != "https" {
		return "", errors.New("registry wants an unknown login")
	}
	q := realm.Query()
	if params["service"] != "" {
		q.Set("service", params["service"])
	}
	q.Set("scope", "repository:"+r.repo+":pull")
	realm.RawQuery = q.Encode()
	res, body, err := c.fetch(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", nil // no anonymous token: the manifest request answers 401 and counts as private
	default:
		return "", fmt.Errorf("token endpoint answered %d", res.StatusCode)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(body, &tok)
	if tok.Token == "" {
		tok.Token = tok.AccessToken
	}
	c.mu.Lock()
	c.tokens[key] = tok.Token
	c.mu.Unlock()
	return tok.Token, nil
}

func (c *checker) check(ctx context.Context, r ref) result {
	res := c.resolve(ctx, r)
	res.CheckedAt = time.Now().Unix()
	return res
}

func (c *checker) resolve(ctx context.Context, r ref) result {
	if r.host != hubRegistry {
		return c.registry(ctx, r, false)
	}
	if r.digest == "" {
		return c.hub(ctx, r)
	}
	// Pinned by digest on Docker Hub: HEAD the digest (HEAD does not count against the
	// anonymous pull limit, GET does), architectures from the Hub API when a tag is named.
	res := c.registry(ctx, r, true)
	if res.Status == "ok" && r.tagSet {
		if h := c.hub(ctx, r); h.Status == "ok" {
			res.Archs = h.Archs
		}
	}
	return res
}

// hub uses the Docker Hub API for tags: it lists the architectures and, unlike manifest GETs,
// does not count against the anonymous pull limit. Private repos look missing (404) there.
func (c *checker) hub(ctx context.Context, r ref) result {
	res, body, err := c.fetch(ctx, http.MethodGet, fmt.Sprintf("%s/v2/repositories/%s/tags/%s", c.hubAPI, r.repo, url.PathEscape(r.tag)), nil)
	if errors.Is(err, errRateLimited) {
		return result{Status: "rate-limited", Detail: "Docker Hub API"}
	}
	if err != nil {
		return result{Status: "error", Detail: err.Error()}
	}
	switch res.StatusCode {
	case http.StatusOK:
		var t struct {
			Images []struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"images"`
		}
		json.Unmarshal(body, &t)
		var archs []string
		for _, im := range t.Images {
			if im.OS == "linux" || im.OS == "" {
				archs = append(archs, im.Architecture)
			}
		}
		return result{Status: "ok", Archs: uniq(archs)}
	case http.StatusNotFound:
		return result{Status: "missing", Detail: "tag or repository not found on Docker Hub (it may also be private)"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return result{Status: "private", Detail: fmt.Sprintf("Docker Hub answered %d", res.StatusCode)}
	}
	return result{Status: "error", Detail: fmt.Sprintf("Docker Hub answered %d", res.StatusCode)}
}

// registry resolves through the OCI distribution API: anonymous token from WWW-Authenticate,
// then the manifest (index platforms, or the config blob for a single-arch image). head only
// checks that the manifest exists.
func (c *checker) registry(ctx context.Context, r ref, head bool) result {
	ver := r.tag
	if r.digest != "" {
		ver = r.digest
	}
	base := fmt.Sprintf("https://%s/v2/%s/", r.host, r.repo)
	method := http.MethodGet
	if head {
		method = http.MethodHead
	}
	get := func(path, accept string) (*http.Response, []byte, error) {
		hdr := map[string]string{"Accept": accept}
		res, body, err := c.fetch(ctx, method, base+path, hdr)
		if err == nil && res.StatusCode == http.StatusUnauthorized {
			tok, terr := c.token(ctx, r, res.Header.Get("Www-Authenticate"))
			if terr != nil {
				return nil, nil, terr
			}
			if tok != "" {
				hdr["Authorization"] = "Bearer " + tok
				res, body, err = c.fetch(ctx, method, base+path, hdr)
			}
		}
		return res, body, err
	}
	res, body, err := get("manifests/"+ver, manifestAccept)
	if errors.Is(err, errRateLimited) {
		return result{Status: "rate-limited", Detail: r.host}
	}
	if err != nil {
		return result{Status: "error", Detail: err.Error()}
	}
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return result{Status: "missing", Detail: r.host + ": manifest unknown"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return result{Status: "private", Detail: fmt.Sprintf("%s answered %d (private or does not exist)", r.host, res.StatusCode)}
	default:
		return result{Status: "error", Detail: fmt.Sprintf("%s answered %d", r.host, res.StatusCode)}
	}
	if head {
		return result{Status: "ok"}
	}
	var m struct {
		Manifests []struct {
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
	}
	json.Unmarshal(body, &m)
	var archs []string
	for _, p := range m.Manifests {
		if p.Platform.OS == "linux" {
			archs = append(archs, p.Platform.Architecture)
		}
	}
	if len(m.Manifests) == 0 && m.Config.Digest != "" {
		if cres, cbody, err := get("blobs/"+m.Config.Digest, "*/*"); err == nil && cres.StatusCode == http.StatusOK {
			var cfg struct {
				Architecture string `json:"architecture"`
			}
			json.Unmarshal(cbody, &cfg)
			if cfg.Architecture != "" {
				archs = []string{cfg.Architecture}
			}
		}
	}
	return result{Status: "ok", Archs: uniq(archs)}
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && s != "unknown" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
