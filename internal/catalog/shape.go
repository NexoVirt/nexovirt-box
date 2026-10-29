package catalog

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Store composes carry mistakes Docker refuses at install time: mixed environment syntax
// (`- KEY: value` is a list of maps: "environment.[0]: unexpected type map"), `network-mode`,
// depends_on naming a service that does not exist, an env_file that only exists on the store's
// own system. FixShapesNode repairs those; ValidateCompose checks what Docker is strict about.

// serviceKeys are the service attributes of the Compose specification.
var serviceKeys = words("annotations attach blkio_config build cap_add cap_drop cgroup cgroup_parent command configs " +
	"container_name cpu_count cpu_percent cpu_period cpu_quota cpu_rt_period cpu_rt_runtime cpu_shares cpus cpuset " +
	"credential_spec depends_on deploy develop device_cgroup_rules devices dns dns_opt dns_search domainname driver_opts " +
	"entrypoint env_file environment expose extends external_links extra_hosts gpus group_add healthcheck hostname image " +
	"init ipc isolation labels label_file links logging mac_address mem_limit mem_reservation mem_swappiness " +
	"memswap_limit models network_mode networks oom_kill_disable oom_score_adj pid pids_limit platform ports post_start " +
	"pre_stop privileged profiles provider pull_policy read_only restart runtime scale secrets security_opt shm_size " +
	"stdin_open stop_grace_period stop_signal storage_opt sysctls tmpfs tty ulimits use_api_socket user userns_mode " +
	"uts volumes volumes_from working_dir")

// FixShapesNode repairs every service. It returns how many things it changed.
func FixShapesNode(doc *yaml.Node) int {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	names := serviceNames(doc)
	fixed := 0
	forEachService(doc, func(_ string, svc *yaml.Node) {
		if svc.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i < len(svc.Content); i += 2 {
			k := svc.Content[i]
			if u := strings.ReplaceAll(k.Value, "-", "_"); u != k.Value && serviceKeys[u] && mapGet(svc, u) == nil {
				k.Value = u
				fixed++
			}
		}
		if ef := mapGet(svc, "env_file"); ef != nil {
			if ef.Kind == yaml.ScalarNode {
				*ef = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: ef.Value}}}
			}
			for i, e := range ef.Content {
				if e.Kind == yaml.ScalarNode && !envFileShipped(e.Value) {
					ef.Content[i] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Tag: "!!str", Value: "path"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Value},
						{Kind: yaml.ScalarNode, Tag: "!!str", Value: "required"}, {Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"},
					}}
					fixed++
				}
			}
		}
		if dep := mapGet(svc, "depends_on"); dep != nil {
			switch dep.Kind {
			case yaml.SequenceNode:
				var kept []*yaml.Node
				for _, e := range dep.Content {
					if names[e.Value] {
						kept = append(kept, e)
					} else {
						fixed++
					}
				}
				dep.Content = kept
			case yaml.MappingNode:
				var kept []*yaml.Node
				for i := 0; i+1 < len(dep.Content); i += 2 {
					if names[dep.Content[i].Value] {
						kept = append(kept, dep.Content[i], dep.Content[i+1])
					} else {
						fixed++
					}
				}
				dep.Content = kept
			}
		}
		env := mapGet(svc, "environment")
		if env == nil || env.Kind != yaml.SequenceNode {
			return
		}
		for i, e := range env.Content {
			if e.Kind != yaml.MappingNode || len(e.Content) != 2 || e.Content[1].Kind != yaml.ScalarNode {
				continue
			}
			k, v := e.Content[0], e.Content[1]
			s := k.Value
			if v.Tag != "!!null" {
				s += "=" + v.Value
			}
			env.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
			fixed++
		}
	})
	return fixed
}

// FixShapes is FixShapesNode on a.Compose; the compose is re-encoded only when something changed.
func FixShapes(a *App) (int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(a.Compose), &root); err != nil || len(root.Content) == 0 {
		return 0, fmt.Errorf("%s: compose not readable", a.ID)
	}
	n := FixShapesNode(&root)
	if n == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root.Content[0]); err != nil {
		return 0, err
	}
	a.Compose = buf.String()
	return n, nil
}

// ValidateCompose checks the structure Docker Compose insists on: services is a map of maps,
// environment and labels are a map of scalars or a list of strings, ports and volumes are lists
// of strings or maps.
func ValidateCompose(compose string) []string {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(compose), &root); err != nil || len(root.Content) == 0 {
		return []string{"compose not readable"}
	}
	doc := root.Content[0]
	if s := mapGet(doc, "services"); s == nil || s.Kind != yaml.MappingNode || len(s.Content) == 0 {
		return []string{"services missing or not a map"}
	}
	var errs []string
	bad := func(svc, field, why string) { errs = append(errs, fmt.Sprintf("services.%s.%s: %s", svc, field, why)) }
	names := serviceNames(doc)
	forEachService(doc, func(name string, svc *yaml.Node) {
		if svc.Kind != yaml.MappingNode {
			bad(name, "", "not a map")
			return
		}
		for i := 0; i < len(svc.Content); i += 2 {
			if k := svc.Content[i].Value; !serviceKeys[k] && !strings.HasPrefix(k, "x-") {
				bad(name, k, "unknown attribute")
			}
		}
		if dep := mapGet(svc, "depends_on"); dep != nil {
			step := 1
			if dep.Kind == yaml.MappingNode {
				step = 2
			}
			for i := 0; i < len(dep.Content); i += step {
				if !names[dep.Content[i].Value] {
					bad(name, "depends_on", "service "+dep.Content[i].Value+" does not exist")
				}
			}
		}
		if ef := mapGet(svc, "env_file"); ef != nil {
			list := []*yaml.Node{ef}
			if ef.Kind == yaml.SequenceNode {
				list = ef.Content
			}
			for _, e := range list {
				if e.Kind == yaml.ScalarNode && !envFileShipped(e.Value) {
					bad(name, "env_file", e.Value+" does not exist on a Box (only .env does)")
				}
			}
		}
		for _, f := range []string{"environment", "labels"} {
			n := mapGet(svc, f)
			if n == nil {
				continue
			}
			switch n.Kind {
			case yaml.MappingNode:
				for i := 1; i < len(n.Content); i += 2 {
					if deref(n.Content[i]).Kind != yaml.ScalarNode {
						bad(name, f+"."+n.Content[i-1].Value, "value is not a plain value")
					}
				}
			case yaml.SequenceNode:
				for i, e := range n.Content {
					if deref(e).Kind != yaml.ScalarNode {
						bad(name, fmt.Sprintf("%s.[%d]", f, i), "must be a KEY=value string")
					}
				}
			default:
				bad(name, f, "must be a map or a list")
			}
		}
		for _, f := range []string{"ports", "volumes"} {
			n := mapGet(svc, f)
			if n == nil {
				continue
			}
			if n.Kind != yaml.SequenceNode {
				bad(name, f, "must be a list")
				continue
			}
			for i, e := range n.Content {
				if e = deref(e); e.Kind != yaml.ScalarNode && e.Kind != yaml.MappingNode {
					bad(name, fmt.Sprintf("%s.[%d]", f, i), "must be a string or a map")
				}
			}
		}
	})
	return errs
}

// envFileShipped: an installed catalog app only has compose.yaml and .env in its folder.
func envFileShipped(p string) bool { return p == ".env" || p == "./.env" }

func serviceNames(doc *yaml.Node) map[string]bool {
	m := map[string]bool{}
	forEachService(doc, func(n string, _ *yaml.Node) { m[n] = true })
	return m
}

func forEachService(doc *yaml.Node, f func(string, *yaml.Node)) {
	s := mapGet(doc, "services")
	if s == nil || s.Kind != yaml.MappingNode {
		return
	}
	for i := 1; i < len(s.Content); i += 2 {
		f(s.Content[i-1].Value, deref(s.Content[i]))
	}
}

// deref follows YAML aliases (`environment: *shared_env`).
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return deref(m.Content[i+1])
		}
	}
	return nil
}

// NeedsHost reports a service with privileged mode, the host network or the host PID namespace.
// The importer skips such apps; catalog-check drops one that a shape fix revealed.
func NeedsHost(compose string) bool {
	var root yaml.Node
	if yaml.Unmarshal([]byte(compose), &root) != nil || len(root.Content) == 0 {
		return false
	}
	found := false
	forEachService(root.Content[0], func(_ string, svc *yaml.Node) {
		if v := mapGet(svc, "privileged"); v != nil && v.Value == "true" {
			found = true
		}
		for _, k := range []string{"network_mode", "pid"} {
			if v := mapGet(svc, k); v != nil && v.Value == "host" {
				found = true
			}
		}
	})
	return found
}
