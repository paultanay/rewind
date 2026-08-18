FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/practical-service .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/practical-service /practical-service
USER nonroot:nonroot
ENTRYPOINT ["/practical-service"]
