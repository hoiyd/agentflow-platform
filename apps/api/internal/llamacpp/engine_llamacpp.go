//go:build cgo && llamacpp

package llamacpp

/*
#cgo pkg-config: llama
#cgo CXXFLAGS: -std=c++17
#cgo darwin LDFLAGS: -lc++
#cgo linux LDFLAGS: -lstdc++

#include <stdlib.h>
#include "wrapper.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

const nativeBufferSize = 8192

type Engine struct {
	mu     sync.Mutex
	ptr    *C.af_llama_engine
	model  string
	closed bool
}

func Available() bool {
	return true
}

func New(config Config) (*Engine, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("validate libllama config: %w", err)
	}

	modelPath := C.CString(config.ModelPath)
	defer C.free(unsafe.Pointer(modelPath))

	nativeConfig := C.af_llama_config{
		model_path:   modelPath,
		context_size: C.uint32_t(config.ContextSize),
		batch_size:   C.uint32_t(config.BatchSize),
		threads:      C.int32_t(config.Threads),
		gpu_layers:   C.int32_t(config.GPULayers),
		temperature:  C.float(config.Temperature),
		top_k:        C.int32_t(config.TopK),
		top_p:        C.float(config.TopP),
		seed:         C.uint32_t(config.Seed),
	}
	errorBuffer := make([]byte, nativeBufferSize)
	ptr := C.af_llama_open(
		&nativeConfig,
		(*C.char)(unsafe.Pointer(&errorBuffer[0])),
		C.size_t(len(errorBuffer)),
	)
	if ptr == nil {
		return nil, fmt.Errorf("open libllama engine: %s", cString(errorBuffer))
	}

	descriptionBuffer := make([]byte, nativeBufferSize)
	if code := C.af_llama_model_description(
		ptr,
		(*C.char)(unsafe.Pointer(&descriptionBuffer[0])),
		C.size_t(len(descriptionBuffer)),
	); code != C.AF_LLAMA_OK {
		C.af_llama_close(ptr)
		return nil, fmt.Errorf("read libllama model description: native code %d", int(code))
	}

	return &Engine{ptr: ptr, model: cString(descriptionBuffer)}, nil
}

func (e *Engine) ModelDescription() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.model
}

func (e *Engine) Generate(
	ctx context.Context,
	prompt string,
	maxTokens int,
	onToken TokenHandler,
) (Stats, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || e.ptr == nil {
		return Stats{}, errors.New("libllama engine is closed")
	}
	if prompt == "" || maxTokens <= 0 {
		return Stats{}, errors.New("prompt and max tokens must be provided")
	}
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
	if onToken == nil {
		onToken = func(string) error { return nil }
	}

	promptCString := C.CString(prompt)
	defer C.free(unsafe.Pointer(promptCString))
	errorBuffer := make([]byte, nativeBufferSize)
	if code := C.af_llama_begin(
		e.ptr,
		promptCString,
		C.int32_t(maxTokens),
		(*C.char)(unsafe.Pointer(&errorBuffer[0])),
		C.size_t(len(errorBuffer)),
	); code != C.AF_LLAMA_OK {
		return Stats{}, nativeError("begin generation", code, errorBuffer)
	}

	stopCancel := make(chan struct{})
	cancelExited := make(chan struct{})
	go func() {
		defer close(cancelExited)
		select {
		case <-ctx.Done():
			C.af_llama_cancel(e.ptr)
		case <-stopCancel:
		}
	}()
	defer func() {
		close(stopCancel)
		<-cancelExited
	}()

	started := time.Now()
	stats := Stats{}
	tokenBuffer := make([]byte, nativeBufferSize)
	for {
		var tokenSize C.size_t
		clear(errorBuffer)
		code := C.af_llama_next(
			e.ptr,
			(*C.char)(unsafe.Pointer(&tokenBuffer[0])),
			C.size_t(len(tokenBuffer)),
			&tokenSize,
			(*C.char)(unsafe.Pointer(&errorBuffer[0])),
			C.size_t(len(errorBuffer)),
		)
		switch code {
		case C.AF_LLAMA_OK:
			token := string(tokenBuffer[:int(tokenSize)])
			stats.GeneratedTokens++
			if err := onToken(token); err != nil {
				C.af_llama_cancel(e.ptr)
				stats.Duration = time.Since(started)
				return stats, fmt.Errorf("handle generated token: %w", err)
			}
		case C.AF_LLAMA_DONE:
			stats.Duration = time.Since(started)
			return stats, nil
		case C.AF_LLAMA_CANCELLED:
			stats.Duration = time.Since(started)
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			return stats, context.Canceled
		default:
			stats.Duration = time.Since(started)
			return stats, nativeError("generate token", code, errorBuffer)
		}
	}
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	if e.ptr != nil {
		C.af_llama_close(e.ptr)
		e.ptr = nil
	}
	e.closed = true
	return nil
}

func nativeError(operation string, code C.int, buffer []byte) error {
	message := cString(buffer)
	if message == "" {
		message = "unknown native error"
	}
	return fmt.Errorf("%s: %s (native code %d)", operation, message, int(code))
}

func cString(buffer []byte) string {
	for i, b := range buffer {
		if b == 0 {
			return string(buffer[:i])
		}
	}
	return string(buffer)
}
