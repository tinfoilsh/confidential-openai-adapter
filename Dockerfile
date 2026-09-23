# syntax=docker/dockerfile:1

FROM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
COPY go.mod go.sum *.go /src/tinfoil-adapter/
WORKDIR /src/tinfoil-adapter
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -buildid=' -o /confidential-openai-adapter .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:1b7b9f0f0e0a1d2155f531db587cc48ec26aaf97ab64364225f5bf18a054e66a
COPY --from=build /confidential-openai-adapter /confidential-openai-adapter
EXPOSE 8443
ENTRYPOINT ["/confidential-openai-adapter"]
