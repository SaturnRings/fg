# fasthttp grace


fasthttp grace 的缩写，是一个支持fasthttp热启动的工具。
Zero downtime restarts for fasthttp server.

# Example

```
    import "github.com/saturn/fg"
```

替换fasthttp.ListenAndServe()， 变成fg.ListenAndServe() 即可。

just replace fasthttp.ListenAndServe() with fg.ListenAndServe()

```
    fg.ListenAndServe("localhost:4243", handler)
```

改动一些输出信息，然后执行：

```
    kill -1 {simple_demo}
    curl http://127.0.0.1:4243
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

you will see "World" in the new shell, the old shell will show "Hello~"


