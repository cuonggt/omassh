// Command seed fills a database with plausible infrastructure for the demo
// recording. Temporary tooling; not part of the shipped binary.
package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: seed <db path> [demo-server-port] [identity]")
	}
	os.Remove(os.Args[1])
	st, err := store.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	// When a demo server is running, Production points at it so the embedded
	// panes in the recording show real shells.
	demoPort, identity := 0, ""
	if len(os.Args) > 2 {
		demoPort, _ = strconv.Atoi(os.Args[2])
	}
	if len(os.Args) > 3 {
		identity = os.Args[3]
	}

	prodGroup := store.Group{Name: "Production", User: "deploy", ProxyJump: "bastion.corp"}
	if demoPort > 0 {
		// A jump host would make these unreachable for the recording.
		prodGroup = store.Group{Name: "Production", User: "deploy", Identity: identity}
	}
	prod, _ := st.PutGroup(prodGroup)
	stg, _ := st.PutGroup(store.Group{Name: "Staging", User: "deploy"})
	home, _ := st.PutGroup(store.Group{Name: "Homelab"})

	prodAddr, prodPort := "10.0.1.%d", 0
	if demoPort > 0 {
		prodAddr, prodPort = "127.0.0.1", demoPort
	}
	addr := func(n int) string {
		if demoPort > 0 {
			return prodAddr
		}
		return fmt.Sprintf(prodAddr, n)
	}

	hosts := []store.Host{
		{Name: "web-01", Addr: addr(11), Port: prodPort, GroupID: prod.ID, Tags: []string{"web", "eu-west"}},
		{Name: "web-02", Addr: addr(12), Port: prodPort, GroupID: prod.ID, Tags: []string{"web", "eu-west"}},
		{Name: "db-01", Addr: addr(20), Port: prodPort, GroupID: prod.ID, Tags: []string{"postgres"}},
		{Name: "stg-web", Addr: "10.0.2.11", GroupID: stg.ID, Tags: []string{"web"}},
		{Name: "stg-db", Addr: "10.0.2.20", GroupID: stg.ID, Tags: []string{"postgres"}},
		{Name: "nas", Addr: "192.168.1.10", Port: 2222, User: "cuonggt", GroupID: home.ID, Tags: []string{"storage"}},
		{Name: "pi-hole", Addr: "192.168.1.11", GroupID: home.ID, Tags: []string{"dns"}},
		{Name: "router", Addr: "192.168.1.1", User: "admin", GroupID: home.ID, Tags: []string{"network"}},
	}
	// Reachable, so the probe has something to report as up. A high port keeps
	// the listener unprivileged.
	if len(os.Args) > 2 {
		hosts = append(hosts, store.Host{
			Name: "workstation", Addr: "127.0.0.1", Port: 2222, GroupID: home.ID, Tags: []string{"local"},
		})
	}

	var dbHost store.Host
	for _, h := range hosts {
		saved, err := st.PutHost(h)
		if err != nil {
			log.Fatal(err)
		}
		if h.Name == "db-01" {
			dbHost = saved
		}
	}

	st.PutForward(store.Forward{HostKey: dbHost.StatKey(), Name: "postgres",
		Kind: store.ForwardLocal, ListenPort: 5432, TargetHost: "localhost", TargetPort: 5432})
	st.PutForward(store.Forward{HostKey: dbHost.StatKey(), Name: "metrics",
		Kind: store.ForwardLocal, ListenPort: 9090, TargetHost: "localhost", TargetPort: 9090})

	st.PutSnippet(store.Snippet{Name: "disk usage", Command: "df -h /"})
	st.PutSnippet(store.Snippet{Name: "uptime", Command: "uptime"})
	st.PutSnippet(store.Snippet{Name: "restart nginx", Command: "sudo systemctl restart nginx"})

	// A little history so the detail pane is not all "never connected".
	st.RecordSession(dbHost.StatKey(), time.Now().Add(-2*time.Hour))
	for range 3 {
		st.RecordSession(dbHost.StatKey(), time.Now().Add(-40*time.Minute))
	}
	fmt.Println("seeded", os.Args[1])
}
