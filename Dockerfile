FROM golang:1.26.4-alpine

# Installation des outils de build indispensables pour CGO / SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Activer CGO pour SQLite
ENV CGO_ENABLED=1

# On copie le fichier go.mod (et go.sum s'il existe)
COPY go.mod go.sum* ./

# NOUVEAU : On télécharge les dépendances lors de la CRÉATION de l'image
RUN go mod download

RUN mkdir -p /app/data

EXPOSE 8080

# Au démarrage, le 'go mod tidy' sera instantané car les dépendances sont déjà dans l'image
CMD sh -c "go mod tidy && go run main.go"