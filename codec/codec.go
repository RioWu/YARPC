package codec

import "io"

type Header struct {
	ServiceMethod string //格式为“Service.Method"
	Seq           uint64 //一个RPC请求的ID，由客户端指定
	Error         string //错误信息，客户端置为空，服务端如果发生错误，将错误信息置于Error中
}
type Codec interface {
	io.Closer
	ReadHeader(*Header) error
	ReadBody(interface{}) error
	Write(*Header, interface{}) error
}
type NewCodecFunc func(io.ReadWriteCloser) Codec

type Type string

const (
	GobType Type = "application/gob"
)

//map的定义方式为 var 名称 map[keytype]valuetype
//用一个map存储不同类型的codec的构造函数
var NewCodecFuncMap map[Type]NewCodecFunc

/*
	init()函数会先于main函数自动执行
	init函数没有输入参数、返回值，也未声明，所以无法引用
	无论包被导入多少次，init函数只会被调用一次，也就是只执行一次
*/
func init() {
	/*
		使用make对map类型的变量进行初始化
		make用来分配并且初始化一个slice,map或者chan对象，并且只能是这三种对象。
		make的返回值就是这个类型，而不是指针
	*/
	NewCodecFuncMap = make(map[Type]NewCodecFunc)
	NewCodecFuncMap[GobType] = NewGobCodec
}
