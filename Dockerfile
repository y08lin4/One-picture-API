FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/one-picture-api .

FROM alpine:3.20

WORKDIR /app
COPY --from=build /out/one-picture-api /usr/local/bin/one-picture-api
COPY public ./public
COPY images/.gitkeep ./images/.gitkeep
COPY images/web/.gitkeep ./images/web/.gitkeep
COPY images/m/.gitkeep ./images/m/.gitkeep
COPY tokens.example.json tags_index.example.json ./

ENV OPAPI_ADDR=:8080
EXPOSE 8080

CMD ["one-picture-api"]
