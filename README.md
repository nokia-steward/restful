# RESTful

## Quick introduction

This Go package is a powerful extension of standard Go HTTP server and client libraries.
It lets you handle HTTP+JSON without any extra code.

[Reference.](https://pkg.go.dev/github.com/nokia/restful)

### Lambda server

You receive and respond with data structures.

```go
type reqData struct {
    Num int `json:"num" validate:"lt=1000000"`
}

type respData struct {
    Number int `json:"number"`
}

func create(ctx context.Context, req *reqData) (*respData, error) {
    // You use data structures directly, without marshalling and unmarshalling.
    resp := respData{Number: req.Num}
    return &resp, nil
}

func main() {
    restful.HandleFunc("/user/v1", create).Methods(http.MethodPost)
    restful.Start()
}
```

### RESTful client

You send and receive data structures.

```go
location, err := restful.Post(ctx, "https://example.com", &reqData, &respData)
```

## Details

* [Lambda server](doc/lambda.md) Focus on business logic. It is a modern variant of an HTTP server.
* [RESTful server](doc/server.md) An underlying HTTP server of Lambda. An HTTP server with goodies.
  Besides helper functions for receiving and sending JSON data, it can do logging.
  Router is based on [Gorilla/Mux](https://github.com/gorilla/mux), offering similar services.
* [RESTful client](doc/client.md) Sending GET, POST (and receiving Location), PUT, PATCH or DELETE requests and receiving their responses.
  And numerous other helper functions.
* [Tracing](doc/tracing.md) Information is propagated in context, received in Lambda, and used in client requests.
  That is all done without any extra coding on your side.
  Based on [OpenTelemetry](https://opentelemetry.io/).
* [Monitor](doc/monitor.md) is a convenient middleware solution to pre-process requests and post-process responses.
  Pre and post hooks can be used for whatever you want, such as adding Prometheus counters on the router level, without littering your business logic.
* [Error](doc/error.md) is a Go error object containing an HTTP status code besides the traditional error.

You may ask why these are packed together instead of being completely independent packages.
A consistent server and client package have the advatage of using the same error package, e.g. a client error can be returned by the server without any mapping.

This package (both client and server part) logs requests and responses separately. That is great as you see when a request arrived/sent with the right timestamp, but you can't see at the status code right away the related request. You can correlate the logs using the tracing IDs printed.

## Principles

* Simple, intuitive, Go-ish.
* Similar to Go's built-in http package, with some advanced router inherited from [Gorilla/Mux](https://github.com/gorilla/mux) project.
* Powerful HTTP+JSON framework reducing development costs while improving quality.
* Has quite many goodies needed for developing complex applications.
