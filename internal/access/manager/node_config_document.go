package manager

import (
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/gin-gonic/gin"
)

// handleNodeConfigDocument exposes only the selected node's redacted startup document.
func (s *Server) handleNodeConfigDocument(c *gin.Context) {
	nodeID, err := parseManagerNodeConfigNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	if s.management == nil {
		writeNodeConfigError(c, managementusecase.ErrNodeConfigUnavailable)
		return
	}
	reader, ok := s.management.(managementusecase.NodeConfigDocumentReader)
	if !ok {
		writeNodeConfigError(c, managementusecase.ErrNodeConfigDocumentUnsupported)
		return
	}
	document, err := reader.NodeConfigDocument(c.Request.Context(), nodeID)
	if err == nil {
		err = document.Validate(nodeID)
	}
	if err != nil {
		writeNodeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeConfigDocumentResponse(document))
}

// NodeConfigDocumentResponse owns the public TOML document HTTP shape.
type NodeConfigDocumentResponse struct {
	GeneratedAt     time.Time                           `json:"generated_at"`
	NodeID          uint64                              `json:"node_id"`
	Source          string                              `json:"source"`
	RequiresRestart bool                                `json:"requires_restart"`
	TOML            string                              `json:"toml"`
	Sections        []NodeConfigDocumentSectionResponse `json:"sections"`
	Fields          []NodeConfigDocumentFieldResponse   `json:"fields"`
}

// NodeConfigDocumentSectionResponse identifies one table header's base line.
type NodeConfigDocumentSectionResponse struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

// NodeConfigDocumentFieldResponse supplies public field help and provenance.
type NodeConfigDocumentFieldResponse struct {
	Path          string `json:"path"`
	EnvKey        string `json:"env_key"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	DescriptionZH string `json:"description_zh"`
	Source        string `json:"source"`
	Line          int    `json:"line"`
	Redacted      bool   `json:"redacted"`
}

func nodeConfigDocumentResponse(d managementusecase.NodeConfigDocument) NodeConfigDocumentResponse {
	response := NodeConfigDocumentResponse{
		GeneratedAt: d.GeneratedAt, NodeID: d.NodeID, Source: d.Source, RequiresRestart: d.RequiresRestart, TOML: d.TOML,
		Sections: make([]NodeConfigDocumentSectionResponse, 0, len(d.Sections)), Fields: make([]NodeConfigDocumentFieldResponse, 0, len(d.Fields)),
	}
	for _, section := range d.Sections {
		response.Sections = append(response.Sections, NodeConfigDocumentSectionResponse{Path: section.Path, Line: section.Line})
	}
	for _, field := range d.Fields {
		response.Fields = append(response.Fields, NodeConfigDocumentFieldResponse{
			Path: field.Path, EnvKey: field.EnvKey, Label: field.Label, Description: field.Description, DescriptionZH: field.DescriptionZH,
			Source: field.Source, Line: field.Line, Redacted: field.Redacted,
		})
	}
	return response
}
