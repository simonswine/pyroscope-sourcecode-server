FROM golang:1.24.5 AS build

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY sourceserver/ ./sourceserver/
RUN CGO_ENABLED=0 go build -o source-server

FROM debian:bookworm

ARG TARGETARCH

# Install dependcies
RUN apt-get update && apt-get install -y git curl build-essential

# Install bazelisk
# TODO: Hash check
RUN curl -Lo bazelisk.deb https://github.com/bazelbuild/bazelisk/releases/download/v1.26.0/bazelisk-${TARGETARCH}.deb && \
    dpkg -i bazelisk.deb && \
    rm bazelisk.deb

RUN usermod -d /data/home -m nobody && mkdir -p /data/home && chown nobody:nogroup /data /data/home

USER nobody
VOLUME /home/nobody

# Copy sourceserver over
COPY --from=build /build/source-server /usr/local/bin/source-server

EXPOSE 8080
CMD ["/usr/local/bin/source-server"]
