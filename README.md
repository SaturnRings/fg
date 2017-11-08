# fg

Zero downtime restarts for fasthttp server.

# Example

```
    import "github.com/saturn/fg"
```

just replace fasthttp.ListenAndServe() with fg.ListenAndServe()

```
    fg.ListenAndServe("localhost:4243", handler)
```

# Demo

```
    go build -o simple_demo example/simple.go
    ./simple_demo
    curl http://127.0.0.1:4243
```

wait for 5s, you will see "Hello~"

Now, change the "Hello~" to "World".  And :

```
    go build -o simple_demo example/simple.go
    curl http://127.0.0.1:4243
```

In a new shell, run 

```
    kill -1 {simple_demo}
    curl http://127.0.0.1:4243
```

you will see "World".


