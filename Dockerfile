FROM golang:1.25-alpine AS build
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -o catalogue .

FROM alpine:3.19
RUN apk update && apk upgrade --no-cache && apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=build /app/catalogue .
EXPOSE 8002
ENV NEW_RELIC_APP_NAME=roboshop-catalogue
ENV NEW_RELIC_DISTRIBUTED_TRACING_ENABLED=true
CMD ["./catalogue"]

#