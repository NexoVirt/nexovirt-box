package main

// Update notices. Apps: the helper compares each image's local digest with the registry's
// current digest for the same tag (a HEAD request, nothing is downloaded). Box itself: a signed
// release manifest (latest.json + latest.json.sig, Ed25519 release key embedded in the binary)
// at the configured update address; applying it is always a click, and a failed health check
// puts the old binary back.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nexovirt/box/internal/catalog"
)

// ---- registry digests ----

type imageRef struct{ host, repo, tag string }

func parseImage(img string) (imageRef, bool) {
	if strings.Contains(img, "@") || img == "" || strings.Contains(img, "$") {
		return imageRef{}, false // pinned by digest (never "updates") or not resolved
	}
	r := imageRef{host: "registry-1.docker.io", tag: "latest"}
	name := img
	if first, rest, ok := strings.Cut(img, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.host, name = first, rest
		if r.host == "docker.io" || r.host == "index.docker.io" {
			r.host = "registry-1.docker.io"
		}
	}
	if c := strings.LastIndex(name, ":"); c > strings.LastIndex(name, "/") {
		name, r.tag = name[:c], name[c+1:]
	}
	if r.host == "registry-1.docker.io" && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	r.repo = name
	return r, r.repo != ""
}

var bearerRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

var registryClient = &http.Client{Timeout: 20 * time.Second}

// remoteDigest asks the registry for the digest the tag points to now.
func remoteDigest(ctx context.Context, r imageRef) (string, error) {
	u := fmt.Sprintf("https://%s/v2/%s/manifests/%s", r.host, r.repo, r.tag)
	accept := "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json"
	do := func(token string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		req.Header.Set("Accept", accept)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return registryClient.Do(req)
	}
	res, err := do("")
	if err != nil {
		return "", err
	}
	res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		params := map[string]string{}
		for _, m := range bearerRe.FindAllStringSubmatch(res.Header.Get("Www-Authenticate"), -1) {
			params[strings.ToLower(m[1])] = m[2]
		}
		realm, err := url.Parse(params["realm"])
		if err != nil || realm.Scheme != "https" {
			return "", errors.New("registry wants an unknown login")
		}
		q := realm.Query()
		q.Set("service", params["service"])
		q.Set("scope", "repository:"+r.repo+":pull")
		realm.RawQuery = q.Encode()
		treq, _ := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
		tres, err := registryClient.Do(treq)
		if err != nil {
			return "", err
		}
		var tok struct {
			Token       string `json:"token"`
			AccessToken string `json:"access_token"`
		}
		json.NewDecoder(io.LimitReader(tres.Body, 64<<10)).Decode(&tok)
		tres.Body.Close()
		if tok.Token == "" {
			tok.Token = tok.AccessToken
		}
		if res, err = do(tok.Token); err != nil {
			return "", err
		}
		res.Body.Close()
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry answered %d", res.StatusCode)
	}
	d := res.Header.Get("Docker-Content-Digest")
	if !strings.HasPrefix(d, "sha256:") {
		return "", errors.New("registry sent no digest")
	}
	return d, nil
}

type appUpdate struct {
	Available bool     `json:"available"`
	Images    []string `json:"images,omitempty"` // images with a newer version
	Error     string   `json:"error,omitempty"`
}

func (h *helperServer) updatesCheck(req helperRequest, logf func(string)) (any, error) {
	if err := needDocker(); err != nil {
		return map[string]appUpdate{}, nil
	}
	entries, _ := os.ReadDir(h.appsDir)
	out := map[string]appUpdate{}
	for _, e := range entries {
		meta, err := h.readMeta(e.Name())
		if err != nil {
			continue
		}
		out[meta.ID] = checkApp(h.appDir(meta.ID), meta.ID)
	}
	return out, nil
}

func checkApp(dir, id string) appUpdate {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := compose(dir, id, "config", "--images")
	raw, err := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...).Output()
	if err != nil {
		return appUpdate{Error: "The app definition could not be read."}
	}
	var u appUpdate
	for _, img := range strings.Fields(string(raw)) {
		ref, ok := parseImage(img)
		if !ok {
			continue
		}
		local, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{json .RepoDigests}}", img).Output()
		if err != nil {
			continue // not pulled yet
		}
		var digests []string
		json.Unmarshal(local, &digests)
		remote, err := remoteDigest(ctx, ref)
		if err != nil {
			u.Error = "The registry could not be asked for " + img + "."
			continue
		}
		have := false
		for _, d := range digests {
			have = have || strings.HasSuffix(d, "@"+remote)
		}
		if !have && len(digests) > 0 {
			u.Available = true
			u.Images = append(u.Images, img)
		}
	}
	return u
}

// ---- Box releases ----

type releaseFile struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type releaseManifest struct {
	Version     string                 `json:"version"`
	Notes       string                 `json:"notes"`
	PublishedAt int64                  `json:"published_at"`
	Files       map[string]releaseFile `json:"files"` // by GOARCH
}

// releaseURLOK allows https everywhere and plain http only on this machine (a local mirror);
// the signature is what makes a release trustworthy either way.
func releaseURLOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	h := u.Hostname()
	return u.Scheme == "http" && (h == "localhost" || net.ParseIP(h).IsLoopback())
}

var releaseClient = &http.Client{Timeout: 5 * time.Minute}

var releaseKey = catalog.ReleasePublicKey // tests swap it

func fetchLimited(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	res, err := releaseClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", u, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("file too large")
	}
	return b, err
}

// fetchManifest downloads and verifies latest.json from the update address.
func fetchManifest(ctx context.Context, base string) (*releaseManifest, error) {
	base = strings.TrimRight(base, "/")
	if !releaseURLOK(base) {
		return nil, userError{"The update address must use https."}
	}
	raw, err := fetchLimited(ctx, base+"/latest.json", 64<<10)
	if err != nil {
		return nil, userError{"The update information could not be downloaded."}
	}
	sigB64, err := fetchLimited(ctx, base+"/latest.json.sig", 1024)
	if err != nil {
		return nil, userError{"The update signature could not be downloaded."}
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || !ed25519.Verify(releaseKey, raw, sig) {
		return nil, userError{"The update is not signed by NexoVirt. It was not used."}
	}
	var m releaseManifest
	if json.Unmarshal(raw, &m) != nil || m.Version == "" {
		return nil, userError{"The update information is not valid."}
	}
	return &m, nil
}

// newerVersion compares dotted numbers; a version with a suffix (0.2.0-dev) sorts before the
// same version without one.
func newerVersion(a, b string) bool {
	pa, sa, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	pb, sb, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")
	na, nb := strings.Split(pa, "."), strings.Split(pb, ".")
	for i := 0; i < max(len(na), len(nb)); i++ {
		var x, y int
		if i < len(na) {
			x, _ = strconv.Atoi(na[i])
		}
		if i < len(nb) {
			y, _ = strconv.Atoi(nb[i])
		}
		if x != y {
			return x > y
		}
	}
	return sa == "" && sb != ""
}

// selfUpdate installs a newer signed release, checks the web service comes back on it, and
// restores the old binary when it does not.
func (h *helperServer) selfUpdate(req helperRequest, logf func(string)) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	logf("Checking the update")
	m, err := fetchManifest(ctx, req.URL)
	if err != nil {
		return nil, err
	}
	if !newerVersion(m.Version, version) {
		return nil, uerr("NexoVirt Box %s is already the newest version.", version)
	}
	f, ok := m.Files[runtime.GOARCH]
	if !ok || !releaseURLOK(f.URL) || len(f.SHA256) != 64 {
		return nil, uerr("This update has no build for %s servers.", runtime.GOARCH)
	}
	logf("Downloading NexoVirt Box " + m.Version)
	bin, err := fetchLimited(ctx, f.URL, 200<<20)
	if err != nil {
		return nil, uerr("The update could not be downloaded.")
	}
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != strings.ToLower(f.SHA256) {
		return nil, uerr("The downloaded file does not match the signed update. It was not used.")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	newPath, bakPath := exe+".new", exe+".bak"
	if err := os.WriteFile(newPath, bin, 0o755); err != nil {
		return nil, err
	}
	if out, err := exec.CommandContext(ctx, newPath, "version").Output(); err != nil || strings.TrimSpace(string(out)) != m.Version {
		os.Remove(newPath)
		return nil, uerr("The new version does not start on this server. Nothing was changed.")
	}
	if err := copyFile(exe, bakPath); err != nil {
		os.Remove(newPath)
		return nil, err
	}
	if err := os.Rename(newPath, exe); err != nil {
		return nil, err
	}
	logf("Restarting the web interface")
	exec.Command("systemctl", "restart", "nexovirt-box").Run()
	if !webHealthy(m.Version, req.HTTPPort) {
		logf("The new version did not answer. Putting the old version back.")
		os.Rename(bakPath, exe)
		exec.Command("systemctl", "restart", "nexovirt-box").Run()
		return nil, uerr("NexoVirt Box %s did not start correctly, so the previous version was restored.", m.Version)
	}
	logf("NexoVirt Box " + m.Version + " is running. The helper restarts in a few seconds.")
	// Restart this helper on the new binary after this answer was sent.
	exec.Command("systemd-run", "--on-active=3", "--unit=nexovirt-box-helper-restart", "systemctl", "restart", "nexovirt-box-helper").Run()
	return map[string]string{"version": m.Version}, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

// webHealthy waits for the web service to answer with the expected version.
func webHealthy(want string, port int) bool {
	if port == 0 {
		port = 8443
	}
	c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // own self-signed cert on loopback
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		for _, scheme := range []string{"https", "http"} {
			res, err := c.Get(fmt.Sprintf("%s://127.0.0.1:%d/api/auth/state", scheme, port))
			if err != nil {
				continue
			}
			var st struct{ Version string }
			json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&st)
			res.Body.Close()
			if st.Version == want {
				return true
			}
		}
		time.Sleep(time.Second)
	}
	return false
}
