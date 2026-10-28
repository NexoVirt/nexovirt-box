package catalog

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Store composes often ship a literal default secret (POSTGRES_PASSWORD: tipi, AUTH_PASSWORD: <uuid>).
// Every Box would then run with the same public value, so FixSecrets turns each literal into a
// generated password question and uses that variable everywhere the literal was the secret.

var (
	uuidRe     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	nonAlnumRe = regexp.MustCompile(`[^A-Za-z0-9]+`)
	plainValRe = regexp.MustCompile(`(?i)^(true|false|yes|no|on|off|none|null|trust|[0-9]{1,5})$`)
	// A key segment that makes the key a setting about a secret, not the secret itself
	// (PASSWORD_FILE, MYSQL_RANDOM_ROOT_PASSWORD, JWT_EXP, AUTH_MODE, NEXTAUTH_URL).
	settingSegs = words("FILE PATH DIR URL URI RANDOM ALLOW REQUIRE REQUIRED ENABLE ENABLED DISABLE DISABLED MODE " +
		"METHOD METHODL TYPE LENGTH LEN MIN MAX EXP EXPIRY EXPIRE EXPIRES EXPIRATION TTL TIME TIMEOUT LIFETIME HOST " +
		"HOSTNAME PORT SECURE ROLE ROLES PROVIDER ALGORITHM ALG ISSUER AUDIENCE HEADER AUTHOR RESET")
	// A last segment that names who logs in (AUTH_USERNAME, KOMF_KOMGA_USER).
	identitySegs = words("USER USERNAME LOGIN NAME ID DB DATABASE EMAIL")
)

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func segments(k string) []string {
	return strings.FieldsFunc(strings.ToUpper(k), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// IsSecretKey reports whether an environment key holds a secret value.
func IsSecretKey(k string) bool {
	segs := segments(k)
	has := false
	for _, s := range segs {
		if settingSegs[s] {
			return false
		}
		has = has || s == "PASS" || s == "PWD" || s == "AUTH" || strings.HasSuffix(s, "KEY") ||
			strings.Contains(s, "PASSWORD") || strings.Contains(s, "PASSWD") || strings.Contains(s, "PASSPHRASE") ||
			strings.Contains(s, "SECRET") || strings.Contains(s, "TOKEN") || strings.Contains(s, "SALT") || strings.Contains(s, "JWT")
	}
	return has && !identitySegs[segs[len(segs)-1]]
}

// accessKeyID: S3_ACCESS_KEY / MINIO_ACCESS_KEY is the account name of an S3 login. A plain word
// there is a user name shared with MINIO_ROOT_USER; only a random-looking one is a secret.
func accessKeyID(k string) bool {
	segs := segments(k)
	n := len(segs)
	return n >= 2 && segs[n-1] == "KEY" && segs[n-2] == "ACCESS" && !strings.Contains(strings.ToUpper(k), "SECRET")
}

// secretPart splits a value into the part to keep and the secret: NEO4J_AUTH=neo4j/password
// keeps "neo4j/".
func secretPart(k, v string) (string, string) {
	v = unquote(v)
	if segs := segments(k); len(segs) > 0 && segs[len(segs)-1] == "AUTH" && !strings.Contains(v, "://") {
		if i := strings.Index(v, "/"); i > 0 {
			return v[:i+1], v[i+1:]
		}
	}
	return "", v
}

// IsLiteralSecret reports whether v, the value of key k, is a fixed secret written into the compose.
func IsLiteralSecret(k, v string) bool {
	_, v = secretPart(k, v)
	if !IsSecretKey(k) || v == "" || strings.Contains(v, "$") || plainValRe.MatchString(v) ||
		strings.Contains(v, "://") || strings.HasPrefix(v, "/") {
		return false
	}
	return !accessKeyID(k) || strong(v)
}

// strong literals (UUIDs, long or mixed strings) never collide with names, so every occurrence is
// the secret. Weak ones (tipi, gitea) are also user, database and host names: only the secret spots change.
func strong(s string) bool {
	if uuidRe.MatchString(s) || len(s) >= 16 {
		return true
	}
	return len(s) >= 8 && strings.IndexFunc(s, unicode.IsDigit) >= 0 && strings.IndexFunc(s, unicode.IsLetter) >= 0
}

type envSpot struct {
	key string
	val *yaml.Node
	pre string // "KEY=" for list syntax
}

func (s envSpot) value() string { return strings.TrimPrefix(s.val.Value, s.pre) }

func (s envSpot) secret() (string, string) { return secretPart(s.key, s.value()) }

// set writes keep+v, keeping quotes that are part of a list entry (KEY="value").
func (s envSpot) set(keep, v string) {
	q := ""
	if old := s.value(); len(old) >= 2 && (old[0] == '"' || old[0] == '\'') && old[len(old)-1] == old[0] {
		q = old[:1]
	}
	s.val.Value = s.pre + q + keep + v + q
	plain(s.val)
}

func plain(n *yaml.Node) {
	if n.Style != yaml.LiteralStyle && n.Style != yaml.FoldedStyle {
		n.Style = 0
	}
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// envSpots lists every environment entry of the compose (map and list syntax, anywhere, so
// x- anchors count too).
func envSpots(doc *yaml.Node) []envSpot {
	var out []envSpot
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == "environment" {
					switch v.Kind {
					case yaml.MappingNode:
						for j := 0; j+1 < len(v.Content); j += 2 {
							if v.Content[j+1].Kind == yaml.ScalarNode {
								out = append(out, envSpot{v.Content[j].Value, v.Content[j+1], ""})
							}
						}
					case yaml.SequenceNode:
						for _, e := range v.Content {
							if key, _, ok := strings.Cut(e.Value, "="); ok && e.Kind == yaml.ScalarNode {
								out = append(out, envSpot{key, e, key + "="})
							}
						}
					}
					continue
				}
				walk(v)
			}
		case yaml.SequenceNode, yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c)
			}
		}
	}
	walk(doc)
	return out
}

// LiteralSecrets lists "KEY=value" for every secret key that still has a literal value.
func LiteralSecrets(compose string) ([]string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &root); err != nil {
		return nil, err
	}
	var out []string
	for _, s := range envSpots(&root) {
		if IsLiteralSecret(s.key, s.value()) {
			_, lit := s.secret()
			out = append(out, s.key+"="+lit)
		}
	}
	return out, nil
}

// FixSecrets replaces every literal default secret in a.Compose with a generated password
// question. One variable per literal, so services that share a password still match. It returns
// the replaced literals (empty when nothing changed; then a.Compose is untouched).
func FixSecrets(a *App) ([]string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(a.Compose), &root); err != nil || len(root.Content) == 0 {
		return nil, fmt.Errorf("%s: compose not readable", a.ID)
	}
	lits := FixSecretsNode(a, &root)
	if len(lits) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root.Content[0]); err != nil {
		return nil, err
	}
	a.Compose = buf.String()
	return lits, nil
}

// FixSecretsNode is FixSecrets on a parsed compose (the importer encodes once: every yaml.v3
// round trip adds blank lines to folded scalars). It adds the questions to a.
func FixSecretsNode(a *App, root *yaml.Node) []string {
	spots := envSpots(root)
	vars := map[string]string{} // literal -> variable
	taken := map[string]bool{}
	for _, q := range a.Questions {
		taken[q.Key] = true
	}
	var lits []string
	for _, s := range spots {
		_, v := s.secret()
		if !IsLiteralSecret(s.key, s.value()) || vars[v] != "" {
			continue
		}
		name := "SECRET_" + strings.Trim(strings.ToUpper(nonAlnumRe.ReplaceAllString(s.key, "_")), "_")
		for n := 2; taken[name]; n++ {
			name = fmt.Sprintf("SECRET_%s_%d", strings.Trim(strings.ToUpper(nonAlnumRe.ReplaceAllString(s.key, "_")), "_"), n)
		}
		taken[name] = true
		vars[v] = name
		lits = append(lits, v)
	}
	if len(lits) == 0 {
		return nil
	}
	for _, lit := range lits {
		ref := "${" + vars[lit] + "}"
		dropGuards(root, lit)
		if strong(lit) {
			// Not inside a name or a ${VAR} reference.
			tok := regexp.MustCompile(`(^|[^A-Za-z0-9_{$])` + regexp.QuoteMeta(lit) + `([^A-Za-z0-9_}]|$)`)
			repl := func(v string) string {
				for i := 0; i < 3 && tok.MatchString(v); i++ { // adjacent matches share a boundary byte
					v = tok.ReplaceAllString(v, "${1}"+strings.ReplaceAll(ref, "$", "$$")+"${2}")
				}
				return v
			}
			isSpot := map[*yaml.Node]bool{}
			for _, s := range spots {
				isSpot[s.val] = true
				if v := repl(s.value()); v != s.value() {
					s.val.Value = s.pre + v // never the KEY= part
					plain(s.val)
				}
			}
			walkValues(root, func(n *yaml.Node) {
				if !isSpot[n] {
					n.Value = repl(n.Value)
				}
			})
			continue
		}
		for _, s := range spots {
			if keep, v := s.secret(); v == lit && IsLiteralSecret(s.key, s.value()) {
				s.set(keep, ref)
			}
		}
		replaceSecretContexts(root, lit, ref)
	}
	for _, lit := range lits {
		q := Question{Key: vars[lit], Label: humanize(strings.TrimPrefix(vars[lit], "SECRET_")),
			Type: "password", Generate: true, Min: 24, Hint: "Generated automatically. Enter your own to replace it."}
		if n := base64Len(lit); n > 0 {
			q.Format, q.Min = "base64", n
		}
		a.Questions = append(a.Questions, q)
	}
	return lits
}

// base64Len is the decoded size of a literal that is clearly a base64 key (ENTE_KEY_ENCRYPTION),
// so Box can generate one of the same shape; 0 otherwise.
func base64Len(s string) int {
	if len(s) < 16 || !strings.ContainsAny(s, "+/=") {
		return 0
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	return len(b)
}

// replaceSecretContexts swaps a weak literal where a string clearly uses it as the secret:
// URL passwords, "password: x" inside config text, redis --requirepass / auth, and the exec-form
// argument after such a flag.
func replaceSecretContexts(root *yaml.Node, lit, ref string) {
	q := regexp.QuoteMeta(lit)
	r := strings.ReplaceAll(ref, "$", "$$")
	res := []*regexp.Regexp{
		regexp.MustCompile(`(://[^:/@\s]*:)` + q + `(@)`),
		regexp.MustCompile(`(?i)((?:password|passwd|secret|token)["']?\s*[:=]\s*["']?)` + q + `([^A-Za-z0-9_-]|$)`),
		regexp.MustCompile(`((?:--requirepass|--masterauth|\bauth|\s-a)\s+["']?)` + q + `([^A-Za-z0-9_-]|$)`),
	}
	flags := map[string]bool{"--requirepass": true, "--masterauth": true, "auth": true, "-a": true, "--password": true}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		switch n.Kind {
		case yaml.ScalarNode:
			for _, re := range res {
				n.Value = re.ReplaceAllString(n.Value, "${1}"+r+"${2}")
			}
		case yaml.MappingNode:
			for i := 1; i < len(n.Content); i += 2 {
				walk(n.Content[i])
			}
		case yaml.SequenceNode, yaml.DocumentNode:
			for i, c := range n.Content {
				if i > 0 && c.Kind == yaml.ScalarNode && c.Value == lit && flags[n.Content[i-1].Value] {
					c.Value = ref
				}
				walk(c)
			}
		}
	}
	walk(root)
}

// guardRe matches a store's own check that refuses to start while a variable still equals the
// public catalog value: ` || [ "$$KEY" = "literal" ]`. Once the literal is generated that check
// would always fire, so it goes; a check for an empty value stays.
func dropGuards(root *yaml.Node, lit string) {
	re := regexp.MustCompile(`\s*\|\|\s*\[\s*"?\$\$\{?[A-Za-z_][A-Za-z0-9_]*\}?"?\s*==?\s*["']?` + regexp.QuoteMeta(lit) + `["']?\s*\]`)
	walkValues(root, func(n *yaml.Node) { n.Value = re.ReplaceAllString(n.Value, "") })
}

func walkValues(n *yaml.Node, f func(*yaml.Node)) {
	switch n.Kind {
	case yaml.ScalarNode:
		f(n)
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			walkValues(n.Content[i], f)
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			walkValues(c, f)
		}
	}
}

func humanize(k string) string {
	w := strings.ToLower(strings.ReplaceAll(k, "_", " "))
	if w == "" {
		return k
	}
	return strings.ToUpper(w[:1]) + w[1:]
}
