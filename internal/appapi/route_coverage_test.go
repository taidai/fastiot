package appapi

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"fastiot/internal/authz"
	"github.com/simpleiot/simpleiot/data"
)

// TestEveryDispatchRouteIsDeclared parses the dispatch switch in ServeHTTP and
// proves every case resolves in the authorization table. The route list is
// derived from the dispatch code itself, so adding a handler without a table
// entry fails this test instead of silently serving unauthenticated actions.
func TestEveryDispatchRouteIsDeclared(t *testing.T) {
	cases := dispatchCases(t)
	if len(cases) < 50 {
		t.Fatalf("parsed only %d dispatch cases, expected the full ServeHTTP switch", len(cases))
	}
	for _, clause := range cases {
		for _, sample := range caseRoutes(t, clause) {
			request := httptest.NewRequest(sample.method, "/api/"+sample.path, nil)
			if _, ok := authz.Required(request); !ok {
				t.Errorf("%s reaches %s /api/%s but has no authz route declaration",
					clauseText(clause), sample.method, sample.path)
			}
		}
	}
}

func TestUndeclaredRouteIsDenied(t *testing.T) {
	base, admin, _, _ := startInputServer(t, t.TempDir(), data.NewID())
	fixture := &pointConfigFixture{base: base, token: admin}
	root := snapshotRoot(t, base, admin)
	viewer := inputAccount(t, fixture, root, "viewer", nil)
	for _, test := range []struct {
		name, token string
	}{
		{"viewer", viewer},
		{"admin", admin},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, body := call(t, http.MethodGet, base+"/not-registered", test.token, nil, nil)
			if status != http.StatusForbidden || !strings.Contains(body, "route_not_declared") {
				t.Fatalf("undeclared route = %d %s, want 403 route_not_declared", status, body)
			}
		})
	}
}

func dispatchCases(t *testing.T) []*ast.CaseClause {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(file), "handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "handler.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var clauses []*ast.CaseClause
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ServeHTTP" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if switchStmt, ok := node.(*ast.SwitchStmt); ok && clauses == nil {
				for _, stmt := range switchStmt.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if ok && clause.List != nil {
						clauses = append(clauses, clause)
					}
				}
				return false
			}
			return true
		})
	}
	return clauses
}

type routeSample struct {
	method string
	path   string
}

type caseConstraints struct {
	exact     string
	segments  map[int]string
	length    int
	minLength int
	methods   []string
}

func caseRoutes(t *testing.T, clause *ast.CaseClause) []routeSample {
	t.Helper()
	var constraints caseConstraints
	for _, expr := range clause.List {
		collectConstraints(t, expr, &constraints)
	}
	var segments []string
	if constraints.exact != "" {
		segments = strings.Split(constraints.exact, "/")
	} else {
		count := constraints.length
		if count == 0 {
			count = constraints.minLength
		}
		if count == 0 {
			t.Fatalf("%s: cannot derive a path", clauseText(clause))
		}
		segments = make([]string, count)
		for i := range segments {
			segments[i] = "sample"
		}
		for index, literal := range constraints.segments {
			if index >= count {
				t.Fatalf("%s: segment index %d outside length %d", clauseText(clause), index, count)
			}
			segments[index] = literal
		}
	}
	methods := constraints.methods
	if len(methods) == 0 {
		methods = []string{http.MethodGet}
	}
	path := strings.Join(segments, "/")
	samples := make([]routeSample, 0, len(methods))
	for _, method := range methods {
		samples = append(samples, routeSample{method: method, path: path})
	}
	return samples
}

func collectConstraints(t *testing.T, expr ast.Expr, constraints *caseConstraints) {
	t.Helper()
	switch node := expr.(type) {
	case *ast.ParenExpr:
		collectConstraints(t, node.X, constraints)
	case *ast.BinaryExpr:
		switch node.Op {
		case token.LAND:
			collectConstraints(t, node.X, constraints)
			collectConstraints(t, node.Y, constraints)
		case token.LOR:
			for _, side := range []ast.Expr{node.X, node.Y} {
				method, ok := methodComparison(side)
				if !ok {
					t.Fatalf("unsupported || operand %s", exprText(side))
				}
				constraints.methods = append(constraints.methods, method)
			}
		case token.EQL:
			collectEquality(t, node, constraints)
		case token.GTR:
			if isLenOfParts(node.X) {
				constraints.minLength = intLiteral(t, node.Y) + 1
				return
			}
			t.Fatalf("unsupported comparison %s", exprText(expr))
		default:
			t.Fatalf("unsupported operator in %s", exprText(expr))
		}
	default:
		t.Fatalf("unsupported dispatch condition %s", exprText(expr))
	}
}

func collectEquality(t *testing.T, expr *ast.BinaryExpr, constraints *caseConstraints) {
	t.Helper()
	if isIdent(expr.X, "path") {
		constraints.exact = stringLiteral(t, expr.Y)
		return
	}
	if method, ok := methodComparison(expr); ok {
		constraints.methods = append(constraints.methods, method)
		return
	}
	if isLenOfParts(expr.X) {
		constraints.length = intLiteral(t, expr.Y)
		return
	}
	if index, ok := expr.X.(*ast.IndexExpr); ok && isIdent(index.X, "parts") {
		position := intLiteral(t, index.Index)
		if constraints.segments == nil {
			constraints.segments = map[int]string{}
		}
		constraints.segments[position] = stringLiteral(t, expr.Y)
		return
	}
	t.Fatalf("unsupported equality %s", exprText(expr))
}

func methodComparison(expr ast.Expr) (string, bool) {
	comparison, ok := expr.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return "", false
	}
	selector, ok := comparison.X.(*ast.SelectorExpr)
	if !ok || !isIdent(selector.X, "r") || selector.Sel.Name != "Method" {
		return "", false
	}
	value, ok := comparison.Y.(*ast.SelectorExpr)
	if !ok || !isIdent(value.X, "http") {
		return "", false
	}
	method := map[string]string{
		"MethodGet":    http.MethodGet,
		"MethodPost":   http.MethodPost,
		"MethodPut":    http.MethodPut,
		"MethodPatch":  http.MethodPatch,
		"MethodDelete": http.MethodDelete,
	}[value.Sel.Name]
	return method, method != ""
}

func isLenOfParts(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	fn, ok := call.Fun.(*ast.Ident)
	return ok && fn.Name == "len" && isIdent(call.Args[0], "parts")
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func stringLiteral(t *testing.T, expr ast.Expr) string {
	t.Helper()
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		t.Fatalf("expected string literal, got %s", exprText(expr))
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func intLiteral(t *testing.T, expr ast.Expr) int {
	t.Helper()
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		t.Fatalf("expected integer literal, got %s", exprText(expr))
	}
	value, err := strconv.Atoi(literal.Value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func exprText(expr ast.Expr) string {
	var buffer bytes.Buffer
	_ = printer.Fprint(&buffer, token.NewFileSet(), expr)
	return buffer.String()
}

func clauseText(clause *ast.CaseClause) string {
	var parts []string
	for _, expr := range clause.List {
		parts = append(parts, exprText(expr))
	}
	return fmt.Sprintf("case %s", strings.Join(parts, ", "))
}
