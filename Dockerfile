FROM golang:1.16-buster as builder

# Create and change to the app directory.
WORKDIR /app

# Retrieve application dependencies.
# This allows the container build to reuse cached dependencies.
# Expecting to copy go.mod and if present go.sum.
COPY go.* ./
RUN go mod download

# Copy local code to the container image.
COPY . ./

# Build the binary.
RUN go build -v -o server
RUN set -x && apt-get update && apt-get install -y nodejs npm
RUN npm install -g npm
RUN npm install -g sass
RUN sass static/style.scss static/style.css
# Use the official Debian slim image for a lean production container.
# https://hub.docker.com/_/debian
# https://docs.docker.com/develop/develop-images/multistage-build/#use-multi-stage-builds
FROM debian:buster-slim
EXPOSE 17777
WORKDIR /app
RUN set -x && apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y \
    ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Copy the binary to the production image from the builder stage.
ENV PB_STATIC_ROOT /app/static
COPY --from=builder /app/server /app/server
COPY ./static /app/static
# Run the web service on container startup:6379.
CMD ["/app/server"]
