package adaptive

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestShadowDefaultOffWhenUnset: sin CAPIMUX_ADAPTIVE_SHADOW (default real de
// una instalación) no se registra nada.
func TestShadowDefaultOffWhenUnset(t *testing.T) {
	t.Setenv("CAPIMUX_ADAPTIVE_SHADOW", "")
	if err := os.Unsetenv("CAPIMUX_ADAPTIVE_SHADOW"); err != nil {
		t.Fatal(err)
	}
	r := Route{Provider: "default-off-test", Model: "demo"}
	for _, v := range []string{"", "0", "true", "yes", "on", "TRUE", " 0 "} {
		if v != "" {
			t.Setenv("CAPIMUX_ADAPTIVE_SHADOW", v)
		}
		ObserveIfEnabled(Observation{Route: r, Latency: time.Second, Success: true})
	}
	if got := ShadowSnapshot([]Route{r})[0].Samples; got != 0 {
		t.Fatalf("shadow registró %d muestras sin opt-in explícito", got)
	}
}

type countingTransport struct{ calls atomic.Int64 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, http.ErrHandlerTimeout
}

// TestShadowEnabledMakesNoOutboundRequests: aun con la telemetría activada,
// observar, exportar y servir el export no hacen ninguna petición saliente.
func TestShadowEnabledMakesNoOutboundRequests(t *testing.T) {
	rt := &countingTransport{}
	prevT, prevC := http.DefaultTransport, http.DefaultClient
	http.DefaultTransport = rt
	http.DefaultClient = &http.Client{Transport: rt}
	t.Cleanup(func() { http.DefaultTransport, http.DefaultClient = prevT, prevC })

	t.Setenv("CAPIMUX_ADAPTIVE_SHADOW", "1")
	r := Route{Provider: "no-egress-test", Model: "demo"}
	for i := 0; i < 50; i++ {
		ObserveIfEnabled(Observation{Route: r, Latency: time.Millisecond, Success: i%2 == 0, Tokens: 10, CostUSD: 0.01})
	}
	if _, err := ShadowExportJSON(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	NewExportHandler(func(*http.Request) bool { return true }).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/shadow", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d", w.Code)
	}
	if n := rt.calls.Load(); n != 0 {
		t.Fatalf("shadow hizo %d peticiones salientes", n)
	}
}

// TestAdaptivePackageHasNoNetworkClient: guardia estática. El código de
// producción del paquete no puede usar clientes HTTP, sockets ni procesos, así
// que no puede emitir datos fuera del box ni llamar APIs pagas.
func TestAdaptivePackageHasNoNetworkClient(t *testing.T) {
	forbiddenImports := map[string]bool{"net": true, "net/rpc": true, "net/smtp": true, "os/exec": true, "net/http/httputil": true, "syscall": true}
	forbiddenHTTP := map[string]bool{"Get": true, "Post": true, "PostForm": true, "Head": true, "NewRequest": true, "NewRequestWithContext": true, "Client": true, "DefaultClient": true, "Transport": true, "DefaultTransport": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if forbiddenImports[p] {
				t.Errorf("%s importa %s", f, p)
			}
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "http" && forbiddenHTTP[sel.Sel.Name] {
					t.Errorf("%s usa http.%s (cliente saliente)", fset.Position(sel.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no se revisó ningún archivo")
	}
}
