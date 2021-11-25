package YARPC

import (
	"go/ast"
	"log"
	"reflect"
)

type methodType struct {
	method    reflect.Method //方法本身
	ArgType   reflect.Type   //第一个参数的类型
	ReplyType reflect.Type   //第二个参数的类型
}

type service struct {
	name   string                 //映射的结构体的名称
	typ    reflect.Type           //结构体的类型
	rcvr   reflect.Value          //结构体的实例本身，保留rcvr是因为在调用时需要rcvr作为第0个参数
	method map[string]*methodType //method是map类型，用来存储映射的结构体的所有符合条件的方法
}

func (m *methodType) newArgv() reflect.Value {
	var argv reflect.Value
	//argv可能是一个指针类型，或者值类型
	if m.ArgType.Kind() == reflect.Ptr {
		//这里的Elem方法返回该指针或者接口具体指向的类型
		//假设ArgType是string*,则最终返回的是一个代表string*的reflect.value
		//注：这里不能写成reflect.New(m.ArgType).Elem()
		//因为虽然这个式子返回的仍然是一个代表string*的reflect.value
		//但由于reflect.New()方法创建的是一个指向对应类型零值的指针，string类型的零值是"",
		//可以创建一个指向它的指针，这个指针的值是一个地址，而string*类型的零值则是nil,指向它的指针的值是一个指向nil的string**,再取Elem()，得到的是一个值为nil的string*
		argv = reflect.New(m.ArgType).Elem()
	} else {
		//reflect.Value.Elem()用于获取一个指针对象的真正的值
		//New returns a Value representing a pointer to a new zero value for the specified type.
		//That is, the returned Value’s Type is PtrTo(type).
		//因此，这里使用reflect.Value.Elem()的返回值就是type
		//也就是说，假设ArgType是string,不使用reflect.Value.Elem()，返回的是一个代表string*的reflect.value
		//使用了之后，返回的是一个代表string的reflect.value
		//注：这里同样也不能写成reflect.New(m.ArgType.Elem())，因为对一个代表string的reflect.type再使用Elem()是不被允许的
		argv = reflect.New(m.ArgType).Elem()
		//总结：使用reflect.New()方法时，确保传入的参数Type不是指针
	}
	return argv
}
func (m *methodType) newReplyv() reflect.Value {
	//返回值一定是一个指针类型
	replyv := reflect.New(m.ReplyType.Elem())
	switch m.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}
	return replyv
}

func newService(rcvr interface{}) *service {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr)
	//reflect.Indirect(v reflect.Value)用于获取v指向的值
	s.name = reflect.Indirect(s.rcvr).Type().Name()
	s.typ = reflect.TypeOf(rcvr)
	//ast.IsExported()判断该类型是不是exported的
	//ast -> Abstract Syntax Tree
	if !ast.IsExported(s.name) {
		//只有类型为exported(首字母大写)，才可以被注册
		log.Fatalf("rpc server: %s is not a valie service name", s.name)
	}
	s.registerMethods()
	return s
}
func (s *service) registerMethods() {
	s.method = make(map[string]*methodType)
	//遍历寻找符合注册条件的方法
	for i := 0; i < s.typ.NumMethod(); i++ {
		method := s.typ.Method(i)
		mType := method.Type
		//如果该方法参数不为3个(第0个参数是自身，类似于python的self，java的this，则继续
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		//如果该方法返回值不是error类型
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}
		argType, replyType := mType.In(1), mType.In(2)
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}
}
func isExportedOrBuiltinType(t reflect.Type) bool {
	//PkgPath()返回包名
	return ast.IsExported(t.Name()) || t.PkgPath() == ""
}
func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	f := m.method.Func
	//[]reflect.Value{s.rcvr, argv, replyv}是go语言中的匿名数组
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv})
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}
