// fieldlist_test.go provides fieldNames, a small reflection helper every
// field-allowlist test in this package uses (Aufgabe 8-11's own new
// mandate, applied retroactively to Aufgabe 5-7's hand-written output
// structs too — see read_document_test.go, read_boxes_test.go,
// read_binary_test.go, read_account_test.go for its call sites).
//
// A hand-written tool (one not routed through registerReadService/
// registerSync, read_generic.go/read_sync.go) has NO automatic
// PoisonProbe check at registration — that check is what catches a
// leaking field for the generic descriptors, and it simply never runs
// for a bespoke handler. Proven the hard way in the review of Aufgabe
// 5-7 (read.go's own "get_document, sync_documents,
// list_document_conversations" section doc comment and its Gegenprobe):
// a second foreign-text field added to getDocumentOutput ran the entire
// existing test suite green. A fixed, hand-maintained field allowlist is
// the cheapest thing that turns "someone adds a field later" into a
// build-time-adjacent test failure instead of a silent leak — it does
// not know WHICH fields are safe (that judgement stays with whoever
// wrote the struct and its allowlist), it only refuses to let the set
// change without a human noticing.
package tools

import "reflect"

// fieldNames returns v's exported field names, in declaration order — v
// must be a struct value (not a pointer). Used by every
// TestXxxFeldlisteIstAbgeschlossen test in this package to compare
// against a fixed allowlist; a mismatch (added, removed, or reordered
// field) fails the test, forcing whoever changed the struct to update
// the allowlist as a deliberate, visible edit rather than a side effect.
func fieldNames(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		names = append(names, t.Field(i).Name)
	}
	return names
}
