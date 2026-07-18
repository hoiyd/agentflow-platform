#ifndef AGENTFLOW_LIBLLAMA_WRAPPER_H
#define AGENTFLOW_LIBLLAMA_WRAPPER_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct af_llama_engine af_llama_engine;

typedef struct {
    const char * model_path;
    uint32_t context_size;
    uint32_t batch_size;
    int32_t threads;
    int32_t gpu_layers;
    float temperature;
    int32_t top_k;
    float top_p;
    uint32_t seed;
} af_llama_config;

enum {
    AF_LLAMA_OK = 0,
    AF_LLAMA_DONE = 1,
    AF_LLAMA_CANCELLED = 2,
    AF_LLAMA_ERROR_INVALID_ARGUMENT = -1,
    AF_LLAMA_ERROR_MODEL_LOAD = -2,
    AF_LLAMA_ERROR_CONTEXT = -3,
    AF_LLAMA_ERROR_TOKENIZE = -4,
    AF_LLAMA_ERROR_DECODE = -5,
    AF_LLAMA_ERROR_BUFFER = -6
};

af_llama_engine * af_llama_open(
    const af_llama_config * config,
    char * error,
    size_t error_capacity);

void af_llama_close(af_llama_engine * engine);

int af_llama_model_description(
    const af_llama_engine * engine,
    char * output,
    size_t output_capacity);

int af_llama_begin(
    af_llama_engine * engine,
    const char * prompt,
    int32_t max_tokens,
    char * error,
    size_t error_capacity);

int af_llama_next(
    af_llama_engine * engine,
    char * token_piece,
    size_t token_piece_capacity,
    size_t * token_piece_size,
    char * error,
    size_t error_capacity);

void af_llama_cancel(af_llama_engine * engine);

#ifdef __cplusplus
}
#endif

#endif
