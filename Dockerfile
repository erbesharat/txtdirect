FROM alpine:3.9

RUN apk --no-cache add ca-certificates

ADD txtdirect /txtdirect

CMD ["/txtdirect"]