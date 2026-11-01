package protocol

import "strconv"

// Append encodes v as RESP2 and appends it to dst, returning the extended
// slice. Writing into a caller-owned buffer lets a client batch several
// replies (pipeline) and flush them with a single write.
//
// SimpleString and Error are line-based, so any CR or LF in Str is replaced
// with a space; otherwise a message built from user input could split into
// several replies and corrupt the stream.
func Append(dst []byte, v Value) []byte {
	switch v.Kind {
	case SimpleString, Error:
		dst = append(dst, byte(v.Kind))
		for _, b := range v.Str {
			if b == '\r' || b == '\n' {
				b = ' '
			}
			dst = append(dst, b)
		}
		return append(dst, '\r', '\n')
	case Integer:
		dst = append(dst, ':')
		dst = strconv.AppendInt(dst, v.Int, 10)
		return append(dst, '\r', '\n')
	case BulkString:
		if v.Null {
			return append(dst, "$-1\r\n"...)
		}
		dst = append(dst, '$')
		dst = strconv.AppendInt(dst, int64(len(v.Str)), 10)
		dst = append(dst, '\r', '\n')
		dst = append(dst, v.Str...)
		return append(dst, '\r', '\n')
	case Array:
		if v.Null {
			return append(dst, "*-1\r\n"...)
		}
		dst = append(dst, '*')
		dst = strconv.AppendInt(dst, int64(len(v.Array)), 10)
		dst = append(dst, '\r', '\n')
		for _, elem := range v.Array {
			dst = Append(dst, elem)
		}
		return dst
	default:
		panic("protocol: Append called with unknown Kind")
	}
}
