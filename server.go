package YARPC

import (
	"YARPC/codec"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"reflect"
	"strings"
	"sync"
)

const MagicNumber = 0x3bef5c

type Option struct {
	//MagicNumber表示这是一个YA-RPC请求
	MagicNumber int
	//客户端可以选择不同的codec进行解码
	CodecType codec.Type
}
type Server struct {
	serviceMap sync.Map
}

//request存储了来自一次call的所有信息
type request struct {
	h            *codec.Header //一个请求的Header部分
	argv, replyv reflect.Value //一个请求的argv和replyv部分
	mtype        *methodType
	svc          *service
}

var DefaultOption = &Option{
	MagicNumber: MagicNumber,
	CodecType:   codec.GobType,
}

/*
报文格式如下：
| Option{MagicNumber: xxx, CodecType: xxx} | Header{ServiceMethod ...} | Body interface{} |
| <------      固定 JSON 编码      ------>  | <-------   编码方式由 CodeType 决定   ------->|
在一次连接中，Option固定在报文的最开始，Header和Body可以有多个，即报文的可能形式为：
| Option | Header1 | Body1 | Header2 | Body2 | ...
*/

func NewServer() *Server {
	return &Server{}
}

var DefaultServer = NewServer()

func (server *Server) Accept(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server:accept error:", err)
			return
		}
		go server.ServeConn(conn)
	}
}

//为DefaultServer设置的Accept方法
func Accept(lis net.Listener) { DefaultServer.Accept(lis) }

func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	//"_"的作用是占位符，比如当需要获取某个函数的返回值时，如果该函数有多个返回值，而现在只需要其中的一部分，就可以将不使用的返回值用"_"表示，因为如果使用变量表示，而后续的代码中又没有使用，编译器会报错
	defer func() { _ = conn.Close() }()
	var opt Option
	//解析报文中为json格式的option部分
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error: ", err)
		return
	}
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}
	//serveCodec用来进一步解析报文中的其他部分
	server.serveCodec(f(conn))
}

//当发生错误时，无效的请求应该设置成一个占位符，以方便响应结果的返回，这里使用空struct作为占位符
var invalidRequest = struct{}{}

/*
serveCodec的过程为：
1.读取请求 readRequest
2.处理请求 handleRequest
3.回复请求 sendResponse
*/
func (server *Server) serveCodec(cc codec.Codec) {
	//处理请求可以是并发的，但对请求的回复必须是逐个发送的，如果并发会导致多个回复报文交织在一起导致客户端无法解析，这里使用锁来解决这个问题
	sending := new(sync.Mutex)
	//等待，直到所有的请求处理完成
	wg := new(sync.WaitGroup)

	//在一次连接中可能会收到多个请求，因此使用for循环无限制地等待请求的到来，直到发生错误(如连接被关闭，或接收到了错误报文)
	for {
		req, err := server.readRequest(cc)
		if err != nil {
			if req == nil {
				break
			}
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		//更新sync.WaitGroup中的计数器counter
		wg.Add(1)
		//使用协程并发地执行请求
		//go关键字放在方法调用前新建一个goroutine并让它执行方法体
		go server.handleRequest(cc, req, sending, wg)
	}
	//sync.WaitGroup.Wait会在计数器大于0并且不存在等待的Goroutine时，将该进程置为睡眠
	wg.Wait()
	_ = cc.Close()
}

// 未更新service.go前的readRequest()
// func (server *Server) readRequest(cc codec.Codec) (*request, error) {
// 	h, err := server.readRequestHeader(cc)
// 	if err != nil {
// 		return nil, err
// 	}
// 	req := &request{h: h}
// 	//TODO：目前我们并不知道请求中的argv的类型，先默认为string
// 	req.argv = reflect.New(reflect.TypeOf(""))
// 	if err = cc.ReadBody(req.argv.Interface()); err != nil {
// 		log.Println("rpc server: read argv err:", err)
// 	}
// 	return req, nil
// }

//readRequest()中最重要的部分，是通过newArgv()和newReplyv()两个方法创建出两个入参实例，
//然后通过cc.ReadBody()将请求报文反序列化为第一个入参argv,这里需要注意argv可能是值类型，也可能是指针类型，处理方式有些差异
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()

	//确保argvi是一个指针，ReadBody需要指针作为参数
	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read body err:", err)
		return req, err
	}
	return req, nil
}
func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		//如果错误信息不是关于EOF的，通过log打印出错误信息
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error: ", err)
		}
		return nil, err
	}
	return &h, nil
}
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	//通过req.svc.call完成方法调用
	err := req.svc.call(req.mtype, req.argv, req.replyv)
	if err != nil {
		req.h.Error = err.Error()
		server.sendResponse(cc, req.h, invalidRequest, sending)
		return
	}
	//将replyv传递给sendResponse完成序列化
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}

// 未更新service.go前的handleRequest()
// func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
// 	//TODO：应该调用已经注册的 RPC methods来得到正确的replyv,目前，先打印出argv并且发送一个hello message
// 	//sync.WaitGroup.Done：向sync.WaitGroup.Add方法传入了-1
// 	defer wg.Done()
// 	log.Println(req.h, req.argv.Elem())
// 	req.replyv = reflect.ValueOf(fmt.Sprintf("YARPC resp %d", req.h.Seq))
// 	//reflect.Value.Interface()：从反射值对象reflect.Value中获取原值
// 	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
// }

func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	//使用互斥锁保证服务端对请求的回复是逐个发送而不是并发的
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

//Register方法在服务器上发布满足以下条件的方法
//	-类型为exported
//	-两个参数，均为exported
//	-第二个参数是一个指针
//	-只有一个类型为error的返回值
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

//DefaultServer的Register
func Register(rcvr interface{}) error {
	return DefaultServer.Register(rcvr)
}

//findService()的逻辑：
//	1.ServiceMethod的构成是"Service.Method"，因此先将其分割成2部分，第一部分是Service的名称，第二部分即方法名
//	2.先在serviceMap中找到对应的service实例，再从service实例的method中，找到对应的methodType
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server:can't find service " + serviceName)
		return
	}
	svc = svci.(*service)
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}
	return
}
