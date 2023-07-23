FROM golang:alpine3.17 as builder

WORKDIR /app
COPY . /app

RUN go get -d -v

# Statically compile our app for use in a distroless container
# RUN CGO_ENABLED=0 go build -ldflags="-w -s" -v -o app .

ENTRYPOINT ["go", "run", "main.go"]