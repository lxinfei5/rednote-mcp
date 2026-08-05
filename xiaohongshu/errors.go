package xiaohongshu

import "errors"

// ErrInvalidArgument 表示调用方传入了不支持的参数。
var ErrInvalidArgument = errors.New("invalid argument")
