FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -o /out/seed ./cmd/seed \
 && CGO_ENABLED=0 go build -trimpath -o /out/sim ./cmd/simulator \
 && CGO_ENABLED=0 go build -trimpath -o /out/loadtest ./cmd/loadtest

FROM alpine:3.21
RUN adduser -D -u 10001 app
COPY --from=build /out/ /usr/local/bin/
USER app
EXPOSE 8080
ENTRYPOINT ["api"]
