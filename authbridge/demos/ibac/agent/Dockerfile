# Multi-stage build for the IBAC demo agent.
# The agent has zero non-stdlib dependencies (matches the original
# huang195/ibac repo) so the build context is just main.go + go.mod.

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY agent/main.go ./agent/main.go
RUN CGO_ENABLED=0 go build -o /out/agent ./agent

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agent /agent
EXPOSE 8080
ENTRYPOINT ["/agent"]
