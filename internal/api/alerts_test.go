// Tests de los endpoints de alertas (ADR-006). Cero red: las webhook_url de
// los casos felices usan IP literal pública — el validador real las aprueba
// sin resolver DNS ni discar; las rechazadas son loopback/privadas literales.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/faborubio/faro/internal/store"
)

const goodHook = "https://93.184.216.34/hook"

func (f *fakeStore) CreateAlert(ctx context.Context, token, code string, op store.Operator, threshold float64, webhookURL string) (store.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.alerts == nil {
		f.alerts = make(map[string]store.Alert)
	}
	a := store.Alert{
		ID:            int64(len(f.alerts) + 1),
		Token:         token,
		IndicatorCode: code,
		Operator:      op,
		Threshold:     threshold,
		WebhookURL:    webhookURL,
		Active:        true,
		CreatedAt:     date(2026, 7, 10),
	}
	f.alerts[token] = a
	return a, nil
}

func (f *fakeStore) GetAlertByToken(ctx context.Context, token string) (store.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.alerts[token]
	if !ok {
		return store.Alert{}, fmt.Errorf("alerta: %w", store.ErrNotFound)
	}
	return a, nil
}

func (f *fakeStore) DeleteAlertByToken(ctx context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.alerts[token]; !ok {
		return fmt.Errorf("alerta: %w", store.ErrNotFound)
	}
	delete(f.alerts, token)
	return nil
}

func postAlert(t *testing.T, url string, body string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(url+"/api/alerts", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/alerts: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func TestCreateAlert(t *testing.T) {
	ts, fs := newTestServer(t, 0)
	resp, body := postAlert(t, ts.URL,
		`{"indicator":"dolar","operator":"gt","threshold":1000,"webhook_url":"`+goodHook+`"}`)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, quiero 201; cuerpo: %s", resp.StatusCode, body)
	}
	var got alertResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("respuesta no es JSON: %v", err)
	}
	if len(got.Token) != 64 {
		t.Errorf("token = %q (%d chars), quiero 64 hex de crypto/rand", got.Token, len(got.Token))
	}
	if got.Indicator != "dolar" || got.Operator != "gt" || got.Threshold != 1000 || !got.Active {
		t.Errorf("alerta creada = %+v, no coincide con lo pedido", got)
	}
	if _, ok := fs.alerts[got.Token]; !ok {
		t.Error("la alerta no quedó en el store")
	}
}

func TestCreateAlertUmbralCeroEsValido(t *testing.T) {
	// CASE-005: 0 es un valor legítimo (el IPC puede valer 0,0%); un umbral 0
	// no puede confundirse con "falta threshold".
	ts, _ := newTestServer(t, 0)
	resp, body := postAlert(t, ts.URL,
		`{"indicator":"dolar","operator":"lt","threshold":0,"webhook_url":"`+goodHook+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("umbral 0: status = %d, quiero 201; cuerpo: %s", resp.StatusCode, body)
	}
}

func TestCreateAlertValidaciones(t *testing.T) {
	ts, _ := newTestServer(t, 0)
	cases := []struct {
		name string
		body string
		want string // fragmento esperado del mensaje de error
	}{
		{"no JSON", `esto no es json`, "cuerpo inválido"},
		{"operador inválido", `{"indicator":"dolar","operator":"gte","threshold":1,"webhook_url":"` + goodHook + `"}`, "operator"},
		{"sin threshold", `{"indicator":"dolar","operator":"gt","webhook_url":"` + goodHook + `"}`, "threshold"},
		{"sin webhook_url", `{"indicator":"dolar","operator":"gt","threshold":1}`, "webhook_url"},
		{"indicador desconocido", `{"indicator":"bitcoin","operator":"gt","threshold":1,"webhook_url":"` + goodHook + `"}`, "indicador desconocido"},
		{"webhook loopback (SSRF)", `{"indicator":"dolar","operator":"gt","threshold":1,"webhook_url":"http://127.0.0.1/hook"}`, "privada o reservada"},
		{"webhook metadata cloud (SSRF)", `{"indicator":"dolar","operator":"gt","threshold":1,"webhook_url":"http://169.254.169.254/latest"}`, "privada o reservada"},
		{"webhook esquema ftp", `{"indicator":"dolar","operator":"gt","threshold":1,"webhook_url":"ftp://93.184.216.34/hook"}`, "http o https"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := postAlert(t, ts.URL, tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, quiero 400; cuerpo: %s", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), tc.want) {
				t.Errorf("error = %s, quiero que mencione %q", body, tc.want)
			}
		})
	}
}

func TestGetAndDeleteAlertByToken(t *testing.T) {
	ts, _ := newTestServer(t, 0)
	_, body := postAlert(t, ts.URL,
		`{"indicator":"uf","operator":"lt","threshold":40000,"webhook_url":"`+goodHook+`"}`)
	var created alertResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("crear: %v", err)
	}

	resp, body := get(t, ts.URL+"/api/alerts/"+created.Token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET por token: status = %d; cuerpo: %s", resp.StatusCode, body)
	}
	var got alertResponse
	_ = json.Unmarshal(body, &got)
	if got.Indicator != "uf" || got.Threshold != 40000 {
		t.Errorf("GET devolvió %+v, no coincide con lo creado", got)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/alerts/"+created.Token, nil)
	dresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d, quiero 204", dresp.StatusCode)
	}

	resp, _ = get(t, ts.URL+"/api/alerts/"+created.Token)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET tras borrar: status = %d, quiero 404", resp.StatusCode)
	}
}

func TestAlertTokenDesconocido(t *testing.T) {
	ts, _ := newTestServer(t, 0)

	resp, _ := get(t, ts.URL+"/api/alerts/nadie")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET token desconocido: status = %d, quiero 404", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/alerts/nadie", nil)
	dresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE token desconocido: status = %d, quiero 404", dresp.StatusCode)
	}
}

func TestRutasDeIndicadoresSiguenVivas(t *testing.T) {
	// El sub-mux de alertas no debe robarse las rutas de indicadores.
	ts, _ := newTestServer(t, 0)
	resp, _ := get(t, ts.URL+"/api/dolar")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/dolar: status = %d, quiero 200", resp.StatusCode)
	}
	resp, _ = get(t, ts.URL+"/api/dolar/history")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/dolar/history: status = %d, quiero 200", resp.StatusCode)
	}
}
