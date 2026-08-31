FROM golang:1.26.4-alpine

WORKDIR /app

# Driver SQLite en Go pur : aucun compilateur C nécessaire
ENV CGO_ENABLED=0

# On copie les fichiers de dépendances
COPY go.mod go.sum ./

# Pas de "go mod download" ici : en dev, les modules et le cache de build sont
# montés depuis ./.gocache (voir docker-compose.yml), ce qui les rend
# persistants entre les relances et évite de re-télécharger à chaque rebuild.

RUN mkdir -p /app/data

EXPOSE 8080

CMD ["go", "run", "main.go"]
