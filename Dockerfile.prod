FROM golang:1.26.4-alpine

# Installation des outils de build indispensables pour CGO
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copie d'abord les fichiers de dépendances
COPY go.mod ./

# Force Go à télécharger et valider le driver SQLite proprement
RUN go get github.com/mattn/go-sqlite3 

# Copie le reste du code
COPY main.go ./

# Force CGO et compile avec des retours d'erreurs clairs
ENV CGO_ENABLED=1
RUN go build -v -o veille-app main.go

RUN mkdir -p /app/data

EXPOSE 8080

CMD ["./veille-app"]