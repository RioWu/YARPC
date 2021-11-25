package YARPC

import (
	"YARPC/codec"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

//结构体Call代表一次活跃的RPC调用
type Call struct {
	Seq           uint64
	ServiceMethod string      //格式为"<service>.<method>"
	Args          interface{} //调用参数
	Reply         interface{} //函数返回值
	Error         error       //函数调用如果发生错误，该值应该就会被设定
	Done          chan *Call  //告知本次调用是否已经完成，用于支持异步调用
}

//当调用结束时，通过调用call.Done()去通知调用方
func (call *Call) done() {
	call.Done <- call
}

type Client struct {
	cc       codec.Codec //消息的编码解码器，用来序列化将要发送出去的请求，以及反序列化接收到的响应
	opt      *Option
	sending  sync.Mutex   //互斥锁，和服务端类似，用于保证请求的有序发送，防止多个请求报文混淆
	header   codec.Header //由于请求发送是互斥的，因此每个客户端只需要一个，可以复用，即在多次请求中使用一个header
	mu       sync.Mutex
	seq      uint64           //每个请求拥有一个唯一的编号
	pending  map[uint64]*Call //存储未处理完的请求，键是编号，值是Call实例
	closing  bool             //用户端调用了Close
	shutdown bool             //服务端要求停止程序，一般是有错误发生
}

var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("conection is shut down")

//关闭连接
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing {
		return ErrShutdown
	}
	client.closing = true
	return client.cc.Close()
}

//判断客户端是否正常工作
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.shutdown && !client.closing
}

//将参数call添加到client.pending中，并更新client.seq
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	// if !client.IsAvailable() {
	// 	return 0, ErrShutdown
	// }
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	call.Seq = client.seq
	client.pending[call.Seq] = call
	client.seq++
	return call.Seq, nil
}

//根据seq，从client.pending中移除对应的call，并返回该call
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

//服务端或者客户端发生错误时调用，将shutdown设置为true，并将错误信息通知给所有pending状态的call
func (client *Client) terminateCalls(err error) {
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}
		call := client.removeCall(h.Seq)
		switch {
		case call == nil:
			//call不存在，可能是请求没有发送完整，或者因为其他原因被取消，但服务端仍旧处理了
			err = client.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done()
		default:
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done()
		}
	}
	//发生了错误，终止所有在pending的calls
	client.terminateCalls(err)
}

//parseOption:解析option
//使用可变参数 name ...Type,可变参数在函数中将转换为对应的[]Type类型
func parseOptions(opts ...*Option) (*Option, error) {
	//如果opts为空，或者未传入opts
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}

//用户使用Dial函数传入服务端地址，创建Client实例，为了简化用户调用，这里将opts设置为可选参数
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	//如果client为nil，关闭连接
	//1.这里注意defer的一个性质，return之后的语句先执行，而defer后的语句后执行
	//2.然而，如果defer中修改了要返回的值，该值返回给上层函数时仍然是被defer修改后的结果
	//理解：第1点中return之后的语句先执行，并不是说return操作会在defer之前执行，而是return之后的语句先执行，函数将返回值传递给上层调用者仍然是整个函数运行的最后一步
	defer func() {
		if client == nil {
			_ = conn.Close()
		}
	}()
	return NewClient(conn, opt)
}
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	//发送option
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options error: ", err)
		_ = conn.Close()
		return nil, err
	}
	return newClientCodec(f(conn), opt), nil
}

func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1, //序列号从1开始，0表示不合法的call
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	go client.receive()
	return client
}
func (client *Client) send(call *Call) {
	//保证客户端可以发送一个完整的请求
	client.sending.Lock()
	defer client.sending.Unlock()

	//注册这次call
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	//准备请求的header
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	//编码并发送请求
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		call := client.removeCall(seq)
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

//Go和Call是客户端暴露给用户的两个RPC服务调用接口，Go是一个异步接口，返回call实例
//Call是对Go的封装，阻塞call.Done,等待响应返回，是一个同步接口
func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		//参数10指定了chan的长度
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}
func (client *Client) Call(serviceMethod string, args, reply interface{}) error {
	//<-ch用来从channel ch中接受数据，这个表达式会一直阻塞，直到有数据可以接受
	call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}
