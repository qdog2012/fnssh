FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/fnssh .

FROM alpine:3.20

RUN adduser -D -H fnssh
USER fnssh

ENV FNSSH_ADDR=:5123
EXPOSE 5123

COPY --from=build /out/fnssh /usr/local/bin/fnssh
ENTRYPOINT ["fnssh"]
