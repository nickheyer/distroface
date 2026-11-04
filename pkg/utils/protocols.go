package utils

import "net/http"

// Cleartext http/2 for grpc clients using prior knowledge
func CleartextProtocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetHTTP2(true)
	p.SetUnencryptedHTTP2(true)
	return p
}
