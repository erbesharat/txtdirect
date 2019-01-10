# build stage
FROM golang:1.11-alpine3.8 AS build-binary

RUN apk --no-cache add make git sed \
  && mkdir -p $GOPATH/src/github.com/txtdirect/txtdirect

WORKDIR $GOPATH/src/github.com/txtdirect/txtdirect

COPY . .

# git remote name used by go get -d -u
ARG git_remote=origin
ENV GIT_REMOTE=$git_remote

# git remote address used by go get -d -u
ARG git_remote_address=https://github.com/txtdirect/txtdirect.git
ENV GIT_REMOTE_ADDRESS=$git_remote_address

# git upstream used by go get -d -u
ARG build_upstream=origin/master
ENV UPSTREAM=$build_upstream

RUN make build

# run stage
FROM alpine:3.8
RUN apk --no-cache add ca-certificates
COPY --from=build-binary /go/src/github.com/txtdirect/txtdirect/txtdirect /caddy
CMD ["/caddy"]