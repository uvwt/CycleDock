FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cycledock ./cmd/cycledock \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cycledock-migrate ./cmd/migrate

FROM alpine:3.22
RUN addgroup -S cycledock && adduser -S -G cycledock cycledock
WORKDIR /app
COPY --from=backend /out/cycledock /usr/local/bin/cycledock
COPY --from=backend /out/cycledock-migrate /usr/local/bin/cycledock-migrate
COPY db/migrations /app/db/migrations
COPY --from=web /src/web/dist /app/web
USER cycledock
EXPOSE 8080
ENTRYPOINT ["cycledock"]
