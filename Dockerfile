FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/openclaw ./cmd/openclaw

FROM alpine:3.23

RUN apk add --no-cache ca-certificates

WORKDIR /opt/openclaw

COPY --from=build /out/openclaw /usr/local/bin/openclaw

EXPOSE 8080

ENTRYPOINT ["openclaw"]
CMD ["serve", "--listen", "0.0.0.0:8080"]
