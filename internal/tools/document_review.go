package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/strausmann/go-fileee/fileee"
)

type reviewGetter[T any] interface {
	Get(context.Context, string) (*T, error)
}

// The opaque reference IDs need not be ObjectIDs (built-in type/scheme IDs
// are names), but each must stay one bounded, route-safe path segment.
var reviewIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func validateReviewID(id string) error {
	if !reviewIDPattern.MatchString(id) {
		return fmt.Errorf("id must be one non-empty, route-safe Fileee identifier")
	}
	return nil
}

func reviewTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func reviewDocumentInDashboard(doc *fileee.Document) bool {
	return doc.Attributes.Read != nil && !*doc.Attributes.Read && !doc.Deleted &&
		doc.Status != fileee.DocumentStatusDeleted && doc.Status != fileee.DocumentStatusDeletedPermanently &&
		doc.Status != fileee.DocumentStatusUploading && doc.Status != fileee.DocumentStatusError
}

type reviewDocumentSummary struct {
	ID             string   `json:"id"`
	Version        int64    `json:"version"`
	Status         string   `json:"status"`
	Created        string   `json:"created"`
	Modified       string   `json:"modified"`
	DocumentTypeID string   `json:"documentTypeId"`
	SenderID       string   `json:"senderId"`
	ReceiverID     string   `json:"receiverId"`
	TagIDs         []string `json:"tagIds"`
	// Archived is attributes.data.read.value, null when the flag is absent.
	Archived    *bool `json:"archived"`
	InDashboard bool  `json:"inDashboard"`
	// Reviewed is a separate Fileee flag; it is not the Dashboard/archive state.
	Reviewed *bool `json:"reviewed"`
}

func reviewSummary(doc *fileee.Document) reviewDocumentSummary {
	return reviewDocumentSummary{
		ID: doc.ID, Version: doc.Version, Status: string(doc.Status), Created: reviewTime(doc.Created), Modified: reviewTime(doc.Modified),
		DocumentTypeID: doc.Attributes.DocumentTypeID, SenderID: doc.Attributes.SenderID, ReceiverID: doc.Attributes.ReceiverID,
		TagIDs: append([]string{}, doc.Attributes.TagIDs...), Archived: doc.Attributes.Read, InDashboard: reviewDocumentInDashboard(doc), Reviewed: doc.Attributes.Reviewed,
	}
}

func reviewCanEdit(doc *fileee.Document) error {
	if doc.Deleted || doc.Status == fileee.DocumentStatusDeleted || doc.Status == fileee.DocumentStatusDeletedPermanently || doc.Status == fileee.DocumentStatusUploading || doc.Status == fileee.DocumentStatusError {
		return fmt.Errorf("document is not currently editable")
	}
	for _, action := range doc.ForbiddenActions {
		if action == fileee.ActionEdit {
			return fmt.Errorf("Fileee forbids editing this document")
		}
	}
	return nil
}

func decodeReviewWrapper(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	return fields
}

func reviewWrapperString(wrapper map[string]json.RawMessage, key string) string {
	var text string
	_ = json.Unmarshal(wrapper[key], &text)
	return text
}
