package codec

import (
	"bufio"
	//gob是golong包中自带的一个数据结构序列化的编码/解码工具
	//全称为 go binary
	"encoding/gob"
	"io"
	"log"
)

type GobCodec struct {
	//ReadWriteCloser是一个集合了Reader Writer Closer三个单方法接口的接口
	conn io.ReadWriteCloser
	//为了防止阻塞而创建的带缓冲的Writer，有利于提高性能
	buf *bufio.Writer
	dec *gob.Decoder
	enc *gob.Encoder
}

var _ Codec = (*GobCodec)(nil)

func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn),
		enc:  gob.NewEncoder(buf),
	}
}

func (c *GobCodec) ReadHeader(h *Header) error {
	//decode报文中的Header并将结果存放至变量h中
	return c.dec.Decode(h)
}

func (c *GobCodec) ReadBody(body interface{}) error {
	//decode报文中的Body并将结果存放至变量body中
	return c.dec.Decode(body)
}

func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	//defer关键字用于资源的释放，会在函数返回之前进行调用
	defer func() {
		_ = c.buf.Flush()
		if err != nil {
			_ = c.Close()
		}
	}()
	//将h中的数据编码并通过buf写入到conn中
	if err := c.enc.Encode(h); err != nil {
		log.Println("rpc codec: gob error encoding header:", err)
		return err
	}
	//将body中的数据编码并通过buf写入到conn中
	if err := c.enc.Encode(body); err != nil {
		log.Println("rpc codec: gob error encoding body:", err)
		return err
	}
	return nil
}
func (c *GobCodec) Close() error {
	return c.conn.Close()
}
