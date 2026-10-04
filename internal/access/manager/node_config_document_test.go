package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

type managerDocumentStub struct {
	managerNodesStub
	err  error
	seen *uint64
}

func (s managerDocumentStub) NodeConfigDocument(_ context.Context, nodeID uint64) (management.NodeConfigDocument, error) {
	if s.seen != nil {
		*s.seen = nodeID
	}
	return management.NodeConfigDocument{NodeID: nodeID, Source: management.NodeConfigSnapshotSourceEffectiveStartup,
		TOML:     "[cluster]\nhash_slot_count = 256\n# join_token: hidden\n",
		Sections: []management.NodeConfigDocumentSection{{Path: "cluster", Line: 1}},
		Fields: []management.NodeConfigDocumentField{{Path: "cluster.hash_slot_count", Line: 2},
			{Path: "cluster.join_token", Line: 3, Redacted: true}},
	}, s.err
}

func TestManagerNodeConfigDocumentPermissionTargetAndUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name, resource, path string
		err                  error
		status               int
	}{
		{"read", "cluster.node", "/manager/nodes/2/config/toml", nil, http.StatusOK},
		{"forbidden", "cluster.slot", "/manager/nodes/2/config/toml", nil, http.StatusForbidden},
		{"invalid target", "cluster.node", "/manager/nodes/0/config/toml", nil, http.StatusBadRequest},
		{"old node", "cluster.node", "/manager/nodes/2/config/toml", management.ErrNodeConfigDocumentUnsupported, http.StatusNotImplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen uint64
			srv := New(Options{Auth: testAuthConfig([]UserConfig{{Username: "admin", Password: "secret",
				Permissions: []PermissionConfig{{Resource: tc.resource, Actions: []string{"r"}}},
			}}), Management: managerDocumentStub{err: tc.err, seen: &seen}})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
			rec := httptest.NewRecorder()
			srv.Engine().ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.status == http.StatusOK && (seen != 2 || !strings.Contains(rec.Body.String(), "hash_slot_count = 256")) {
				t.Fatal("missing selected-node TOML")
			}
			if tc.status == http.StatusNotImplemented && !strings.Contains(rec.Body.String(), "node_config_toml_unsupported") {
				t.Fatal("missing capability error")
			}
			if tc.status == http.StatusForbidden && seen != 0 {
				t.Fatal("called provider without permission")
			}
		})
	}
}
