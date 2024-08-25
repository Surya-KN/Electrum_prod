FROM golang:1.22 AS build-stage

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN CGO_ENABLED=0 GOOS=linux go build -o /electrum ./cmd/main.go


FROM gcr.io/distroless/base-debian11 AS build-release-stage

WORKDIR /

COPY --from=build-stage /electrum /electrum

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/electrum"]