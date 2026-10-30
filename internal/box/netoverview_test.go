package box

import "testing"

func TestNetworkOverviewParsing(t *testing.T) {
	ds := parseNeigh([]byte(`[{"dst":"10.10.10.1","dev":"eth0","lladdr":"a6:36:72:8f:4b:de","state":["REACHABLE"]},
		{"dst":"10.10.10.9","dev":"eth0","state":["FAILED"]},{"dst":"172.17.0.2","dev":"docker0","lladdr":"02:42:ac:11:00:02","state":["STALE"]},
		{"dst":"fe80::1","dev":"eth0","lladdr":"aa:bb:cc:dd:ee:ff","state":["STALE"]}]`))
	if len(ds) != 1 || ds[0].IP != "10.10.10.1" || ds[0].State != "reachable" {
		t.Fatalf("%+v", ds)
	}
	ss := `tcp LISTEN 0 4096 0.0.0.0:8171 0.0.0.0:* users:(("docker-proxy",pid=1,fd=4))
tcp LISTEN 0 4096 [::]:8171 [::]:* users:(("docker-proxy",pid=2,fd=4))
tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=3,fd=3))
udp UNCONN 0 0 127.0.0.54:53 0.0.0.0:* users:(("systemd-resolve",pid=344,fd=20))`
	ls := parseSS(ss)
	if len(ls) != 3 || ls[0].Port != 22 || ls[0].Process != "sshd" || ls[1].Port != 53 || ls[1].Proto != "udp" {
		t.Fatalf("%+v", ls)
	}
	apps := appPorts("nxbox-it-tools|0.0.0.0:8171->80/tcp, [::]:8171->80/tcp\nother|0.0.0.0:9000->9000/tcp")
	if apps["tcp/8171"] != "it-tools" || apps["tcp/9000"] != "" {
		t.Fatalf("%v", apps)
	}
}
