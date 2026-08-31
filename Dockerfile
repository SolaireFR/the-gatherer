FROM golang:1.26.4-alpine

WORKDIR /app

# Driver SQLite en Go pur : aucun compilateur C nécessaire
ENV CGO_ENABLED=0

# On copie les fichiers de dépendances
COPY go.mod go.sum ./

# On télécharge les dépendances lors de la CRÉATION de l'image
RUN go mod download

RUN mkdir -p /app/data

EXPOSE 8080

CMD ["go", "run", "main.go"]
