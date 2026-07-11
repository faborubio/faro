// Endpoints de alertas por webhook (ADR-006): registro sin login, gestión por
// token opaco — el token que devuelve el POST es la única llave para consultar
// o borrar la alerta. La webhook_url pasa el anti-SSRF (SAD §8) antes de tocar
// la base; la evaluación y el disparo viven en internal/alert, no aquí.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/faborubio/faro/internal/store"
)

// maxAlertBody acota el JSON del registro: los campos legítimos caben de
// sobra en 4 KB.
const maxAlertBody = 4 << 10

// maxWebhookURLLen acota la URL en la base (la columna es text, sin tope).
const maxWebhookURLLen = 2048

// URLValidator decide si una webhook_url es registrable (implementado por
// internal/webhook; el error se muestra al cliente tal cual).
type URLValidator interface {
	ValidateURL(raw string) error
}

// createAlertRequest es el cuerpo del POST /api/alerts.
type createAlertRequest struct {
	Indicator  string   `json:"indicator"`
	Operator   string   `json:"operator"` // gt | lt
	Threshold  *float64 `json:"threshold"`
	WebhookURL string   `json:"webhook_url"`
}

// alertResponse es la vista pública de una alerta (la ve solo quien tiene el
// token, que viaja en ella misma).
type alertResponse struct {
	Token           string  `json:"token"`
	Indicator       string  `json:"indicator"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	WebhookURL      string  `json:"webhook_url"`
	Active          bool    `json:"active"`
	CreatedAt       string  `json:"created_at,omitempty"`
	LastTriggeredAt string  `json:"last_triggered_at,omitempty"` // vacío = nunca disparó
}

func alertToResponse(a store.Alert) alertResponse {
	resp := alertResponse{
		Token:      a.Token,
		Indicator:  a.IndicatorCode,
		Operator:   string(a.Operator),
		Threshold:  a.Threshold,
		WebhookURL: a.WebhookURL,
		Active:     a.Active,
	}
	if !a.CreatedAt.IsZero() {
		resp.CreatedAt = a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !a.LastTriggeredAt.IsZero() {
		resp.LastTriggeredAt = a.LastTriggeredAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return resp
}

func (s *Server) handleCreateAlert(w http.ResponseWriter, r *http.Request) {
	var req createAlertRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAlertBody))
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "cuerpo inválido: se espera JSON con indicator, operator, threshold y webhook_url")
		return
	}

	if req.Operator != string(store.OpGreater) && req.Operator != string(store.OpLess) {
		s.writeError(w, http.StatusBadRequest, "operator debe ser gt (supera el umbral) o lt (cae bajo él)")
		return
	}
	if req.Threshold == nil {
		s.writeError(w, http.StatusBadRequest, "falta threshold (el umbral numérico; 0 es válido)")
		return
	}
	if req.WebhookURL == "" || len(req.WebhookURL) > maxWebhookURLLen {
		s.writeError(w, http.StatusBadRequest, "falta webhook_url (http/https, máx. 2048 caracteres)")
		return
	}
	if err := s.validator.ValidateURL(req.WebhookURL); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.GetIndicator(r.Context(), req.Indicator); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusBadRequest, "indicador desconocido: ver los códigos del catálogo (uf, dolar, utm, ipc)")
			return
		}
		s.log.Error("validando indicador de alerta", "error", err)
		s.writeError(w, http.StatusInternalServerError, "error interno")
		return
	}

	created, err := s.store.CreateAlert(r.Context(), newToken(), req.Indicator,
		store.Operator(req.Operator), *req.Threshold, req.WebhookURL)
	if err != nil {
		s.log.Error("creando alerta", "error", err)
		s.writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	body, err := json.Marshal(alertToResponse(created))
	if err != nil {
		s.log.Error("serializando alerta creada", "error", err)
		s.writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	writeBody(w, http.StatusCreated, body, "")
}

func (s *Server) handleGetAlert(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetAlertByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		s.writeStoreError(w, r, err, "alerta no encontrada")
		return
	}
	body, err := json.Marshal(alertToResponse(a))
	if err != nil {
		s.log.Error("serializando alerta", "error", err)
		s.writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	writeBody(w, http.StatusOK, body, "")
}

func (s *Server) handleDeleteAlert(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAlertByToken(r.Context(), r.PathValue("token")); err != nil {
		s.writeStoreError(w, r, err, "alerta no encontrada")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newToken genera el token opaco de una alerta: 32 bytes de crypto/rand en
// hex — inadivinable; la unicidad la respalda además el índice único de la
// migración 002.
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand no falla en la práctica; sin azar no hay tokens seguros
	}
	return hex.EncodeToString(b)
}
