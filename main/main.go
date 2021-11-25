package main

import (
	"YARPC"
	"log"
	"net"
	"strings"
	"sync"
)

type Foo struct {
}

type Args struct{ Num1, Num2 float32 }

func (f Foo) Sum(args Args, reply *float32) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func (f Foo) Uppercase(str string, reply *string) error {
	*reply = strings.ToTitle(str)
	return nil
}

//chan string表示一个可以发送string类型数据的channel
func startServer(addr chan string) {
	var foo Foo
	if err := YARPC.Register(&foo); err != nil {
		log.Fatal("register error:", err)
	}
	//选择一个空闲端口
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on", l.Addr())
	//"<-"：箭头操作符，将数据发送到某个channel中
	addr <- l.Addr().String()
	YARPC.Accept(l)
}

func main() {
	log.SetFlags(0)
	addr := make(chan string)
	go startServer(addr)
	//addr的使用可以确保在服务端端口监听成功之后，客户端再发起请求。
	client, _ := YARPC.Dial("tcp", <-addr)
	defer func() {
		_ = client.Close()
	}()
	//发送请求 & 接受响应
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		args := &Args{Num1: 10.24, Num2: 5.12}
		var reply float32
		if err := client.Call("Foo.Sum", args, &reply); err != nil {
			log.Fatal("call Foo.Sum error:", err)
		}
		log.Printf("%f + %f = %f", args.Num1, args.Num2, reply)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		str := "hello world"
		var reply string
		if err := client.Call("Foo.Uppercase", str, &reply); err != nil {
			log.Fatal("call Foo.Uppercase:", err)
		}
		log.Printf("the uppercase form of the string \"%s\" is \"%s\"", str, reply)
	}()
	wg.Wait()
}

//一个简单的测试codec.go的程序，没有包含client.go中的内容
//chan string表示一个可以发送string类型数据的channel
// func startServer(addr chan string) {
// 	//选择一个空闲端口
// 	l, err := net.Listen("tcp", ":0")
// 	if err != nil {
// 		log.Fatal("network error:", err)
// 	}
// 	log.Println("start rpc server on", l.Addr())
// 	//"<-"：箭头操作符，将数据发送到某个channel中
// 	addr <- l.Addr().String()
// 	YARPC.Accept(l)
// }
//func main(){
// 	addr := make(chan string)
// 	go startServer(addr)

// 	//net.Dial(network,address string)用于建立网络连接
// 	conn, _ := net.Dial("tcp", <-addr)
// 	defer func() {
// 		_ = conn.Close()
// 	}()

// 	time.Sleep(time.Second)
// 	//发送报文中的option部分
// 	//json.NewEncoder(io.Writer).Encode(interface{}) 将数据用json格式进行编码并且写入到io.Writer类型的对象中
// 	_ = json.NewEncoder(conn).Encode(YARPC.DefaultOption)
// 	cc := codec.NewGobCodec(conn)
// 	//发送请求 & 接收回复
// 	for i := 0; i < 5; i++ {
// 		h := &codec.Header{
// 			ServiceMethod: "Foo.Sum",
// 			Seq:           uint64(i),
// 		}
// 		_ = cc.Write(h, fmt.Sprintf("YARPC req %d", h.Seq))
// 		_ = cc.ReadHeader(h)
// 		var reply string
// 		_ = cc.ReadBody(&reply)
// 		log.Println("reply:", reply)
// 	}
//}
