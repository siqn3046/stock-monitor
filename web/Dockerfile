FROM golang:1.22 AS build
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o webapp .

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /app/webapp /app/webapp
COPY --from=build /app/templates /app/templates
ENV PORT=8080
EXPOSE 8080
CMD ["/app/webapp"]
