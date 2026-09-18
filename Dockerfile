# syntax=docker/dockerfile:1

FROM golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS build
COPY --from=sdk / /src/tinfoil-go/
COPY go.mod go.sum *.go /src/tinfoil-adapter/
WORKDIR /src/tinfoil-adapter
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -buildid=' -o /confidential-openai-adapter .

FROM gcr.io/distroless/static-debian12:nonroot@sha256:1b7b9f0f0e0a1d2155f531db587cc48ec26aaf97ab64364225f5bf18a054e66a
COPY --from=build /confidential-openai-adapter /confidential-openai-adapter
EXPOSE 8443
ENTRYPOINT ["/confidential-openai-adapter"]
