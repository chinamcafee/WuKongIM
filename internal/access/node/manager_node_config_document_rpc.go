package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// ManagerNodeConfigDocumentRPCServiceID isolates the TOML wire contract from v1 snapshots.
const ManagerNodeConfigDocumentRPCServiceID uint8 = clusternet.RPCManagerNodeConfigDocument

var nodeConfigDocumentRequestMagic = []byte{'W', 'K', 'V', 'C', 2}
var nodeConfigDocumentResponseMagic = []byte{'W', 'K', 'V', 'c', 2}

type nodeConfigDocumentResponse struct {
	Status   string                        `json:"status"`
	Document management.NodeConfigDocument `json:"document"`
}

// HandleManagerNodeConfigDocumentRPC serves already-redacted TOML on its own versioned service.
func (a *Adapter) HandleManagerNodeConfigDocumentRPC(ctx context.Context, body []byte) ([]byte, error) {
	var req managerNodeConfigRPCRequest
	if err := decodeNodeConfigDocumentFrame(body, nodeConfigDocumentRequestMagic, &req); err != nil {
		return nil, err
	}
	response := nodeConfigDocumentResponse{Status: rpcStatusUnavailable}
	if a != nil {
		if reader, ok := a.managerNodeConfig.(management.NodeConfigDocumentReader); ok {
			document, err := reader.NodeConfigDocument(ctx, req.NodeID)
			if err == nil {
				err = document.Validate(req.NodeID)
			}
			response.Status = managerNodeConfigRPCStatusForError(err)
			if errors.Is(err, management.ErrNodeConfigDocumentUnsupported) {
				response.Status = "unsupported"
			}
			if err == nil {
				response.Document = document
			}
		}
	}
	return encodeNodeConfigDocumentFrame(response, nodeConfigDocumentResponseMagic)
}

// GetManagerNodeConfigDocument never substitutes a v1 snapshot or a different node on failure.
func (c *Client) GetManagerNodeConfigDocument(ctx context.Context, nodeID uint64) (management.NodeConfigDocument, error) {
	if c == nil || c.node == nil {
		return management.NodeConfigDocument{}, management.ErrNodeConfigUnavailable
	}
	body, err := encodeNodeConfigDocumentFrame(managerNodeConfigRPCRequest{NodeID: nodeID}, nodeConfigDocumentRequestMagic)
	if err != nil {
		return management.NodeConfigDocument{}, err
	}
	body, err = c.node.CallRPC(ctx, nodeID, ManagerNodeConfigDocumentRPCServiceID, body)
	if errors.Is(err, clusternet.ErrServiceNotFound) {
		return management.NodeConfigDocument{}, management.ErrNodeConfigDocumentUnsupported
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return management.NodeConfigDocument{}, err
		}
		return management.NodeConfigDocument{}, management.ErrNodeConfigUnavailable
	}
	var response nodeConfigDocumentResponse
	if err := decodeNodeConfigDocumentFrame(body, nodeConfigDocumentResponseMagic, &response); err != nil {
		return management.NodeConfigDocument{}, management.ErrNodeConfigUnavailable
	}
	if response.Status == "unsupported" {
		return management.NodeConfigDocument{}, management.ErrNodeConfigDocumentUnsupported
	}
	if err := managerNodeConfigRPCErrorForStatus(response.Status); err != nil {
		return management.NodeConfigDocument{}, err
	}
	if err := response.Document.Validate(nodeID); err != nil {
		return management.NodeConfigDocument{}, err
	}
	return response.Document, nil
}

func encodeNodeConfigDocumentFrame(value any, magic []byte) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(body) > management.MaxNodeConfigDocumentBytes {
		return nil, management.ErrNodeConfigUnavailable
	}
	out := make([]byte, 0, len(magic)+len(body))
	out = append(out, magic...)
	return append(out, body...), nil
}

func decodeNodeConfigDocumentFrame(body, magic []byte, value any) error {
	if len(body) > management.MaxNodeConfigDocumentBytes+len(magic) || !hasMagic(body, magic) {
		return fmt.Errorf("invalid node config document frame")
	}
	return decodeManagerNodeConfigJSON(body[len(magic):], value)
}
