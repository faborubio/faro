# Dockerfile de Faro (ADR-008): multi-stage, binario estático sobre scratch.
# Todo viaja dentro del binario (migraciones, dashboard, Chart.js — go:embed):
# la imagen final es el binario + certificados CA para hablar HTTPS con la CMF.
# Config por ENV (ADR-009): DATABASE_URL, CMF_API_KEY, PORT, REFRESH_INTERVAL.

FROM golang:1.26-alpine AS build
WORKDIR /src
# Capa de dependencias aparte: se re-descargan solo si cambian go.mod/go.sum.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /faro ./cmd/faro

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /faro /faro
# Sin root aunque no haya /etc/passwd: usuario numérico (nobody).
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/faro"]
