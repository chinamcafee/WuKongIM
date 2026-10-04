package node

import (
	"context"
	"errors"
	"reflect"
	"testing"

	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

type documentReaderStub struct {
	fakeManagerNodeConfigReader
	document management.NodeConfigDocument
}

func (r *documentReaderStub) NodeConfigDocument(_ context.Context, id uint64) (management.NodeConfigDocument, error) {
	r.nodeID = id
	return r.document, r.err
}

func nodeDocumentFixture() management.NodeConfigDocument {
	return management.NodeConfigDocument{NodeID: 2, Source: management.NodeConfigSnapshotSourceEffectiveStartup,
		TOML: "[node]\nid = 2\n", Sections: []management.NodeConfigDocumentSection{{Path: "node", Line: 1}},
		Fields: []management.NodeConfigDocumentField{{Path: "node.id", Line: 2, Description: "Identity.", DescriptionZH: "节点标识。"}},
	}
}

func TestNodeConfigDocumentRPCAndV1RemainIndependent(t *testing.T) {
	reader := &documentReaderStub{document: nodeDocumentFixture(),
		fakeManagerNodeConfigReader: fakeManagerNodeConfigReader{snapshot: management.NodeConfigSnapshot{NodeID: 2}}}
	adapter := New(Options{ManagerNodeConfig: reader})
	rpc := &fakeManagerNodeConfigRPCNode{handler: adapter.HandleManagerNodeConfigDocumentRPC}
	document, err := NewClient(rpc).GetManagerNodeConfigDocument(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document, reader.document) || rpc.serviceID != ManagerNodeConfigDocumentRPCServiceID || reader.nodeID != 2 {
		t.Fatalf("incorrect document or target: %+v", document)
	}
	rpc.handler = adapter.HandleManagerNodeConfigRPC
	old, err := NewClient(rpc).GetManagerNodeConfig(context.Background(), 2)
	if err != nil || old.NodeID != 2 || rpc.serviceID != ManagerNodeConfigRPCServiceID {
		t.Fatalf("v1 changed: %+v %v", old, err)
	}
	encoded, err := encodeNodeConfigDocumentFrame(nodeConfigDocumentResponse{Status: rpcStatusOK, Document: document}, nodeConfigDocumentResponseMagic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeManagerNodeConfigResponse(encoded); err == nil {
		t.Fatal("v1 accepted v2 response")
	}
}

func TestNodeConfigDocumentRPCDistinguishesOldNodeFromOutage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cause, want error
	}{
		{"old node", clusternet.ErrServiceNotFound, management.ErrNodeConfigDocumentUnsupported},
		{"outage", errors.New("dial failed"), management.ErrNodeConfigUnavailable},
		{"canceled", context.Canceled, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := &fakeManagerNodeConfigRPCNode{handler: func(context.Context, []byte) ([]byte, error) { return nil, tc.cause }}
			_, err := NewClient(rpc).GetManagerNodeConfigDocument(context.Background(), 2)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestNodeConfigDocumentRPCRejectsInvalidFramesAndWrongNode(t *testing.T) {
	valid, err := encodeNodeConfigDocumentFrame(managerNodeConfigRPCRequest{NodeID: 2}, nodeConfigDocumentRequestMagic)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{nil, valid[:4], append(append([]byte{}, valid...), 'x'),
		append(append([]byte{}, nodeConfigDocumentRequestMagic...), []byte(`{"node_id":2,"extra":1}`)...),
		make([]byte, management.MaxNodeConfigDocumentBytes+6),
	} {
		if _, err := New(Options{}).HandleManagerNodeConfigDocumentRPC(context.Background(), body); err == nil {
			t.Fatal("accepted malformed frame")
		}
	}
	reader := &documentReaderStub{document: nodeDocumentFixture()}
	reader.document.NodeID = 3
	adapter := New(Options{ManagerNodeConfig: reader})
	_, err = NewClient(&fakeManagerNodeConfigRPCNode{handler: adapter.HandleManagerNodeConfigDocumentRPC}).GetManagerNodeConfigDocument(context.Background(), 2)
	if !errors.Is(err, management.ErrNodeConfigUnavailable) {
		t.Fatalf("wrong target accepted: %v", err)
	}
}
