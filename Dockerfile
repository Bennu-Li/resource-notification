FROM golang:1.17 as builder
WORKDIR /app
COPY . .
RUN make build

FROM alpine 
WORKDIR /app
RUN apk update && apk add tzdata
COPY --from=builder /app/notification /app
COPY --from=builder /app/alert.json /app