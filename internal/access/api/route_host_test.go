package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouteHostFallbackUsesEachRequestAuthority(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "0.0.0.0:5100",
			WSAddr:  "ws://[::]:5200/im%2Fsocket?mode=binary",
			WSSAddr: "wss://0.0.0.0/secure",
		},
		LegacyRouteHostFallback: LegacyRouteHostFallback{TCP: true, WS: true, WSS: true},
	})
	for _, target := range []struct {
		host string
		want string
	}{
		{host: "127.0.0.1:5001", want: "127.0.0.1"},
		{host: "im.example.com:15001", want: "im.example.com"},
		{host: "[2001:db8::1]:5001", want: "[2001:db8::1]"},
		{host: "192.0.2.10", want: "192.0.2.10"},
	} {
		for _, batch := range []bool{false, true} {
			t.Run(target.host+map[bool]string{false: "/route", true: "/route/batch"}[batch], func(t *testing.T) {
				response, route := requestRouteHost(t, srv, target.host, "", batch)
				if route.TCPAddr != target.want+":5100" ||
					route.WSAddr != "ws://"+target.want+":5200/im%2Fsocket?mode=binary" ||
					route.WSSAddr != "wss://"+target.want+"/secure" {
					t.Fatalf("route = %+v, want request host %s with listener ports, scheme and path", route, target.want)
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("request-specific route must not enter a shared cache")
				}
			})
		}
	}
}

func TestRouteHostFallbackPreservesPublishedAndSelectedAddresses(t *testing.T) {
	for _, test := range []struct {
		name     string
		external LegacyRouteAddresses
		fallback LegacyRouteHostFallback
		query    string
	}{
		{name: "explicit wildcard override", external: LegacyRouteAddresses{TCPAddr: "0.0.0.0:15100", WSAddr: "ws://0.0.0.0:15200", WSSAddr: "wss://[::]:15300"}},
		{name: "Linux loopback listener", external: LegacyRouteAddresses{TCPAddr: "127.0.0.1:5100", WSAddr: "ws://127.0.0.1:5200"}, fallback: LegacyRouteHostFallback{TCP: true, WS: true, WSS: true}},
		{name: "concrete listener", external: LegacyRouteAddresses{TCPAddr: "192.0.2.10:5100", WSAddr: "ws://192.0.2.10:5200"}, fallback: LegacyRouteHostFallback{TCP: true, WS: true, WSS: true}},
		{name: "no Gateway", fallback: LegacyRouteHostFallback{TCP: true, WS: true, WSS: true}},
		{name: "intranet", external: LegacyRouteAddresses{TCPAddr: "0.0.0.0:5100", WSAddr: "ws://0.0.0.0:5200"}, fallback: LegacyRouteHostFallback{TCP: true, WS: true}, query: "?intranet=1"},
		{name: "explicit node selector", external: LegacyRouteAddresses{TCPAddr: "[::]:5100", WSAddr: "ws://[::]:5200"}, fallback: LegacyRouteHostFallback{TCP: true, WS: true}, query: "?node_id=2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{
				LegacyRouteExternal: test.external,
				LegacyRouteIntranet: test.external,
				LegacyRouteNodes: map[uint64]LegacyRouteNodeAddresses{
					2: {External: test.external},
				},
				LegacyRouteHostFallback: test.fallback,
			})
			for _, batch := range []bool{false, true} {
				response, route := requestRouteHost(t, srv, "browser.example:5001", test.query, batch)
				if route != test.external {
					t.Fatalf("route = %+v, want unchanged %+v", route, test.external)
				}
				if response.Header().Get("Cache-Control") != "" {
					t.Fatal("static route unexpectedly received request-host cache policy")
				}
			}
		})
	}
}

func TestRouteHostFallbackKeepsProtocolOverridesIndependent(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal:     LegacyRouteAddresses{TCPAddr: "0.0.0.0:15100", WSAddr: "ws://:5200", WSSAddr: "wss://edge.example/im"},
		LegacyRouteHostFallback: LegacyRouteHostFallback{WS: true},
	})
	_, route := requestRouteHost(t, srv, "browser.example:5001", "", false)
	if route != (LegacyRouteAddresses{TCPAddr: "0.0.0.0:15100", WSAddr: "ws://browser.example:5200", WSSAddr: "wss://edge.example/im"}) {
		t.Fatalf("route = %+v", route)
	}
}

func TestRouteHostFallbackRejectsUnusableAuthorities(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0:5001", "[::]:5001", "user@evil.example", "host/path", "host?query", "host#fragment", "bad host", "host:invalid", "::1", ":5001"} {
		t.Run(host, func(t *testing.T) {
			srv := New(Options{
				LegacyRouteExternal:     LegacyRouteAddresses{WSAddr: "ws://0.0.0.0:5200"},
				LegacyRouteHostFallback: LegacyRouteHostFallback{WS: true},
			})
			_, route := requestRouteHost(t, srv, host, "", false)
			if route.WSAddr != "ws://0.0.0.0:5200" {
				t.Fatalf("unusable authority reflected in route: %s", route.WSAddr)
			}
		})
	}
}

func requestRouteHost(t *testing.T, srv *Server, host, query string, batch bool) (*httptest.ResponseRecorder, LegacyRouteAddresses) {
	t.Helper()
	method, path, body := http.MethodGet, "/route", ""
	if batch {
		method, path, body = http.MethodPost, "/route/batch", `["demo"]`
	}
	request := httptest.NewRequest(method, path+query, strings.NewReader(body))
	request.Host = host
	request.RemoteAddr = "198.51.100.77:54321"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-Host", "untrusted.example:443")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Forwarded", "host=untrusted.example;proto=https")
	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("route status = %d body = %s", response.Code, response.Body.String())
	}
	var route legacyUserAddrResponse
	if batch {
		var routes []legacyUserAddrResponse
		if err := json.Unmarshal(response.Body.Bytes(), &routes); err != nil {
			t.Fatal(err)
		}
		if len(routes) != 1 || len(routes[0].UIDs) != 1 || routes[0].UIDs[0] != "demo" {
			t.Fatalf("batch routes = %+v", routes)
		}
		route = routes[0]
	} else if err := json.Unmarshal(response.Body.Bytes(), &route); err != nil {
		t.Fatal(err)
	}
	return response, LegacyRouteAddresses{TCPAddr: route.TCPAddr, WSAddr: route.WSAddr, WSSAddr: route.WSSAddr}
}
