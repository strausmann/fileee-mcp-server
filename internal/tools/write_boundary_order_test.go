// write_boundary_order_test.go is the structural Gegenprobe Fix 3
// (homelab-management-Repo, Review-Befund "Framing-Boundary VOR der
// Mutation") ausdrücklich verlangt: fünf Stellen — createContactFromService/
// updateContactFromService (write.go), createReminderFromService/
// updateReminderFromService (write_people.go), updateDocumentFromService
// (write_documents.go) — erzeugen ihre untrusted-block-Boundary
// (newUntrustedBoundary, read.go) jetzt VOR dem jeweiligen mutierenden
// service.Create/Update-Aufruf, nicht mehr danach (der frühere Zustand:
// wrapUntrustedLines erzeugte die Boundary per crypto/rand ERST in
// updateContactResult/createContactResult/reminderResult/
// updateDocumentResult, also NACH der bereits abgeschlossenen Mutation —
// ein Boundary-Fehler an dieser Stelle hätte eine längst persistierte
// Änderung fälschlich als gescheitert gemeldet und einen duplizierenden
// Retry provoziert).
//
// Eine echte Fehlerinjektion gegen crypto/rand (newUntrustedBoundary,
// read.go) wäre hier unverhältnismäßig invasiv — crypto/rand.Read
// schlägt praktisch nie fehl, und ein eigens dafür eingeführter Seam
// (ein austauschbarer Reader/eine austauschbare Funktionsvariable) wäre
// reine Testinfrastruktur ohne Produktionsnutzen. Stattdessen prüft
// dieser Test STRUKTURELL die Aufrufreihenfolge im Quelltext selbst,
// per go/ast: innerhalb jeder der fünf Funktionen muss der Aufruf von
// newUntrustedBoundary() textuell vor dem Aufruf der mutierenden
// Service-Methode stehen. Für den hier vorliegenden, rein sequentiellen
// Code (kein goroutine-Fan-out, keine Nebenläufigkeit innerhalb dieser
// Funktionen) entspricht die Quelltext-Reihenfolge exakt der
// Ausführungsreihenfolge — Go garantiert innerhalb eines einzelnen
// Kontrollflusses ("as-if-sequential" ohne sichtbare Nebenläufigkeit),
// dass Anweisungen mit beobachtbaren Seiteneffekten (ein Netzwerkaufruf
// über *fileee.Client, ein crypto/rand.Read) in genau der Reihenfolge
// wirken, in der sie im Quelltext stehen. Ein Test, der diese
// Reihenfolge vertauscht, würde HIER rot — ohne dass crypto/rand je
// tatsächlich fehlschlagen müsste.
package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// boundaryOrderCase names one of the five Fix-3 call sites: the source
// file and function to parse, and the mutating service-method name
// (the *fileee.Client sub-service method, e.g. "Create"/"Update") that
// must be called strictly AFTER newUntrustedBoundary within that
// function body.
type boundaryOrderCase struct {
	file       string
	funcName   string
	methodName string
}

var boundaryOrderCases = []boundaryOrderCase{
	{file: "write.go", funcName: "createContactFromService", methodName: "Create"},
	{file: "write.go", funcName: "updateContactFromService", methodName: "Update"},
	{file: "write_people.go", funcName: "createReminderFromService", methodName: "Create"},
	{file: "write_people.go", funcName: "updateReminderFromService", methodName: "Update"},
	{file: "write_documents.go", funcName: "updateDocumentFromService", methodName: "Update"},
}

// TestBoundaryWirdVorDerMutierendenServiceMethodeErzeugt is the
// structural Gegenprobe described in this file's own package doc
// comment. It fails if any of the five *FromService functions calls
// its mutating service method (Create/Update) at or before its own
// newUntrustedBoundary() call — i.e. if the fix from Fix 3 were
// reverted, or a future edit reintroduced the old ordering.
func TestBoundaryWirdVorDerMutierendenServiceMethodeErzeugt(t *testing.T) {
	for _, tc := range boundaryOrderCases {
		t.Run(tc.funcName, func(t *testing.T) {
			fset := token.NewFileSet()
			node, err := parser.ParseFile(fset, tc.file, nil, 0)
			if err != nil {
				t.Fatalf("parser.ParseFile(%q): %v", tc.file, err)
			}

			var fn *ast.FuncDecl
			for _, decl := range node.Decls {
				if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == tc.funcName {
					fn = f
					break
				}
			}
			if fn == nil || fn.Body == nil {
				t.Fatalf("Funktion %s nicht in %s gefunden", tc.funcName, tc.file)
			}

			var boundaryPos, mutatingPos token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if fun.Name == "newUntrustedBoundary" && boundaryPos == token.NoPos {
						boundaryPos = call.Pos()
					}
				case *ast.SelectorExpr:
					if fun.Sel.Name == tc.methodName && mutatingPos == token.NoPos {
						mutatingPos = call.Pos()
					}
				}
				return true
			})

			if boundaryPos == token.NoPos {
				t.Fatalf("%s: kein Aufruf von newUntrustedBoundary() gefunden", tc.funcName)
			}
			if mutatingPos == token.NoPos {
				t.Fatalf("%s: kein Aufruf von .%s(...) gefunden", tc.funcName, tc.methodName)
			}
			if boundaryPos >= mutatingPos {
				t.Errorf("%s: newUntrustedBoundary() (Position %d) steht NICHT vor .%s(...) (Position %d) — "+
					"die Boundary muss VOR der mutierenden Service-Methode erzeugt werden (Fix 3, homelab-management-Repo), "+
					"sonst meldet ein Boundary-Fehler eine bereits persistierte Mutation fälschlich als gescheitert",
					tc.funcName, boundaryPos, tc.methodName, mutatingPos)
			}
		})
	}
}
