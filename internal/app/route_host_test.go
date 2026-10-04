package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

func TestAppDefaultGatewayRouteUsesRequestHost(t *testing.T) {
	for _, test := range []struct {
		name     string
		listen   string
		external string
		want     string
	}{
		{name: "Docker wildcard", listen: "0.0.0.0:5200", want: "ws://browser.example:5200"},
		{name: "Linux loopback", listen: "127.0.0.1:5200", want: "ws://127.0.0.1:5200"},
		{name: "explicit published URL", listen: "0.0.0.0:5200", external: " wss://edge.example:443/im ", want: "wss://edge.example:443/im"},
		{name: "explicit wildcard stays explicit", listen: "0.0.0.0:5200", external: "ws://0.0.0.0:15200", want: "ws://0.0.0.0:15200"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, err := newTestApp(t, Config{
				API: APIConfig{ListenAddr: "127.0.0.1:0", ExternalWSAddr: test.external},
				Gateway: GatewayConfig{Listeners: []gateway.ListenerOptions{
					{Name: "ws-gateway", Network: "websocket", Address: test.listen, Transport: "gnet", Protocol: "wsmux"},
				}},
			}, WithCluster(&fakeCluster{}))
			if err != nil {
				t.Fatal(err)
			}
			api, ok := app.api.(*accessapi.Server)
			if !ok {
				t.Fatalf("API = %T, want *api.Server", app.api)
			}
			request := httptest.NewRequest(http.MethodGet, "http://browser.example:5001/route?uid=demo", nil)
			response := httptest.NewRecorder()
			api.Handler().ServeHTTP(response, request)
			var route struct {
				WSAddr string `json:"ws_addr"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &route); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || route.WSAddr != test.want {
				t.Fatalf("GET /route = %d %s, want ws_addr=%q", response.Code, response.Body.String(), test.want)
			}
		})
	}
}
