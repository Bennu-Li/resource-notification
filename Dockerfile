FROM golang:1.17 as builder
WORKDIR /app
COPY . .
RUN make build

FROM alpine 
WORKDIR /
COPY --from=builder /app/notification /
COPY --from=builder /app/alert.json /
RUN chmod +x /notification
#CMD ["/notification"]
