FROM golang:1.25

ENV DEBIAN_FRONTEND noninteractive
ENV LC_ALL C.UTF-8
ENV LANG C.UTF-8

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
            curl ca-certificates gnupg apt-transport-https git software-properties-common

RUN apt-get update && \
    apt-get install -y --no-install-recommends gcc make

RUN go install golang.org/x/lint/golint@latest
RUN go install golang.org/x/tools/...@latest
RUN apt-get update && \
    apt-get -y --no-install-recommends install pre-commit
RUN echo "deb [trusted=yes] https://repo.goreleaser.com/apt/ /" > /etc/apt/sources.list.d/goreleaser.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends goreleaser
