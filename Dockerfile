FROM golang:1.15-alpine AS builder

# Set necessary environmet variables needed for our image
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

# Move to working directory /build
WORKDIR /build

# Copy and download dependency using go mod
COPY go.mod .
COPY go.sum .
RUN go mod download

# Copy the code into the container
COPY main.go .

# Build the application
RUN go build

# Move to /dist directory as the place for resulting binary folder
WORKDIR /dist

# Copy binary from build to main folder
RUN cp /build/fieldmedic .

# Build a small image
FROM scratch

COPY --from=builder /dist/fieldmedic /

# Command to run
ENTRYPOINT ["/fieldmedic"]