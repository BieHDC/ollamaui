package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/ollama/ollama/api"

	"github.com/wlynxg/anet" // due to android sdk bugginess that exists for over 2 years
)

func searchForHosts(hosts *fyne.Container, selected func(string)) {
	setLabel := func(s string) {
		fyne.Do(func() {
			hosts.Objects[0].(*widget.Label).SetText(s)
			hosts.Refresh()
		})
	}
	appendObject := func(co fyne.CanvasObject) {
		fyne.Do(func() {
			hosts.Objects = append(hosts.Objects, co)
			hosts.Refresh()
		})
	}

	found := findInstances()
	num := 0
	for {
		h, ok := <-found
		if !ok { // we read all
			setLabel((fmt.Sprintf("Done Scanning, found %d", num)))
			break
		}
		if h.err != nil {
			appendObject(widget.NewLabel(h.err.Error()))
			continue
		}

		num++
		appendObject(widget.NewButtonWithIcon(fmt.Sprintf("%s (%s)", h.url.Host, h.version), theme.MoveUpIcon(), func() { selected(h.url.Host) }))
	}
}

type hosterInfo struct {
	url     url.URL
	version string
	err     error
}

// low iq implementation, but its fine
func findInstances() <-chan hosterInfo {
	var wg sync.WaitGroup
	hosters := make(chan hosterInfo)

	go func() {
		defer close(hosters)

		// working around a 2 year old bug
		addrs, err := anet.InterfaceAddrs()
		if err != nil {
			hosters <- hosterInfo{err: fmt.Errorf("fail 1: %s", err)}
			return
		}

		for _, address := range addrs {
			// check the address type and if it is not a loopback the display it
			ipnet, ok := address.(*net.IPNet)
			if ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				cidrAddress := ipnet.String()
				p, err := netip.ParsePrefix(cidrAddress)
				if err != nil {
					//fmt.Printf("invalid cidr: %s, error %v\n", cidrAddress, err)
					continue
				}
				// 8.8.8.8/24 => 8.8.8.0/24
				p = p.Masked()

				addr := p.Addr()
				for {
					if !p.Contains(addr) {
						break
					}

					wg.Add(1)
					go func(addr netip.Addr) {
						base := url.URL{
							Scheme: "http",
							Host:   addr.String() + ":11434",
						}
						version, err := testServer(&base)
						if err == nil {
							hosters <- hosterInfo{url: base, version: version}
						}
						wg.Done()
					}(addr)
					addr = addr.Next()
				}
			}
		}

		wg.Wait() // for close
	}()

	return hosters
}

func testServer(surl *url.URL) (string, error) {
	cl := &http.Client{Timeout: 2000 * time.Millisecond}
	fakeclient := api.NewClient(surl, cl)

	version, err := fakeclient.Version(context.TODO())
	if err != nil {
		return "", err
	} else {
		return version, nil
	}
}

func warmCacheForModel(model string, client *api.Client) {
	req := &api.ChatRequest{
		Model:     model,
		Stream:    new(bool),
		KeepAlive: &api.Duration{Duration: 30 * time.Minute},
	}
	respFunc := func(resp api.ChatResponse) error { return nil }

	// knock knock
	client.Chat(context.TODO(), req, respFunc)
}
