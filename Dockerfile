# ---------- Étape 1 : compilation ----------
FROM golang:1.22-alpine AS build

WORKDIR /src
RUN apk add --no-cache git

COPY go.mod go.sum* ./
RUN go mod download || true

COPY . .
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/passguard ./cmd/server

# ---------- Étape 2 : image finale ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && adduser -D -u 10001 passguard
WORKDIR /app

COPY --from=build /out/passguard /app/passguard

# Dossier de régénération manuelle des QR codes (bouton admin) : créé et
# rendu inscriptible pour l'utilisateur non-root AVANT le changement d'USER,
# car un volume Docker monté ici appartient à root par défaut.
RUN mkdir -p /qr_codes && chown -R passguard:passguard /qr_codes

USER passguard
EXPOSE 8080

# Les templates, migrations et fichiers statiques sont embarqués dans le
# binaire (go:embed) : l'image ne contient que l'exécutable.
ENTRYPOINT ["/app/passguard"]
