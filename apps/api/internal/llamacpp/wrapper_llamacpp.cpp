//go:build cgo && llamacpp

#include "wrapper.h"

#include "llama.h"

#include <algorithm>
#include <atomic>
#include <cstring>
#include <mutex>
#include <new>
#include <string>
#include <vector>

struct af_llama_engine {
    llama_model * model = nullptr;
    const llama_vocab * vocab = nullptr;
    llama_context * context = nullptr;
    llama_sampler * sampler = nullptr;

    uint32_t context_size = 0;
    uint32_t batch_size = 0;
    int32_t threads = 0;
    float temperature = 0.0f;
    int32_t top_k = 0;
    float top_p = 1.0f;
    uint32_t seed = LLAMA_DEFAULT_SEED;

    std::vector<llama_token> prompt_tokens;
    size_t prompt_offset = 0;
    llama_token pending_token = LLAMA_TOKEN_NULL;
    int32_t max_tokens = 0;
    int32_t generated_tokens = 0;
    bool prompt_pending = false;
    std::atomic<bool> cancelled{false};
};

namespace {

void set_error(char * output, size_t capacity, const std::string & message) {
    if (output == nullptr || capacity == 0) {
        return;
    }
    const size_t copied = std::min(capacity - 1, message.size());
    std::memcpy(output, message.data(), copied);
    output[copied] = '\0';
}

void clear_generation(af_llama_engine * engine) {
    if (engine->sampler != nullptr) {
        llama_sampler_free(engine->sampler);
        engine->sampler = nullptr;
    }
    if (engine->context != nullptr) {
        llama_free(engine->context);
        engine->context = nullptr;
    }
    engine->prompt_tokens.clear();
    engine->prompt_offset = 0;
    engine->pending_token = LLAMA_TOKEN_NULL;
    engine->generated_tokens = 0;
    engine->max_tokens = 0;
    engine->prompt_pending = false;
}

bool abort_decode(void * data) {
    const auto * engine = static_cast<const af_llama_engine *>(data);
    return engine->cancelled.load(std::memory_order_relaxed);
}

int fail(
    af_llama_engine * engine,
    int code,
    const std::string & message,
    char * error,
    size_t error_capacity) {
    set_error(error, error_capacity, message);
    clear_generation(engine);
    return code;
}

}  // namespace

extern "C" af_llama_engine * af_llama_open(
    const af_llama_config * config,
    char * error,
    size_t error_capacity) {
    if (config == nullptr || config->model_path == nullptr || config->model_path[0] == '\0') {
        set_error(error, error_capacity, "model path is required");
        return nullptr;
    }
    if (config->context_size == 0 || config->batch_size == 0 || config->threads <= 0) {
        set_error(error, error_capacity, "context size, batch size, and threads must be positive");
        return nullptr;
    }

    static std::once_flag backend_once;
    std::call_once(backend_once, [] { ggml_backend_load_all(); });

    auto * engine = new (std::nothrow) af_llama_engine();
    if (engine == nullptr) {
        set_error(error, error_capacity, "failed to allocate engine");
        return nullptr;
    }

    llama_model_params model_params = llama_model_default_params();
    model_params.n_gpu_layers = config->gpu_layers;
    engine->model = llama_model_load_from_file(config->model_path, model_params);
    if (engine->model == nullptr) {
        set_error(error, error_capacity, "failed to load GGUF model: " + std::string(config->model_path));
        delete engine;
        return nullptr;
    }

    engine->vocab = llama_model_get_vocab(engine->model);
    if (engine->vocab == nullptr || llama_model_has_encoder(engine->model) || !llama_model_has_decoder(engine->model)) {
        set_error(error, error_capacity, "prototype requires a decoder-only text generation GGUF model");
        llama_model_free(engine->model);
        delete engine;
        return nullptr;
    }
    engine->context_size = config->context_size;
    engine->batch_size = config->batch_size;
    engine->threads = config->threads;
    engine->temperature = config->temperature;
    engine->top_k = config->top_k;
    engine->top_p = config->top_p;
    engine->seed = config->seed;
    return engine;
}

extern "C" void af_llama_close(af_llama_engine * engine) {
    if (engine == nullptr) {
        return;
    }
    engine->cancelled.store(true, std::memory_order_relaxed);
    clear_generation(engine);
    if (engine->model != nullptr) {
        llama_model_free(engine->model);
    }
    delete engine;
}

extern "C" int af_llama_model_description(
    const af_llama_engine * engine,
    char * output,
    size_t output_capacity) {
    if (engine == nullptr || engine->model == nullptr || output == nullptr || output_capacity == 0) {
        return AF_LLAMA_ERROR_INVALID_ARGUMENT;
    }
    const int32_t written = llama_model_desc(engine->model, output, output_capacity);
    return written < 0 ? AF_LLAMA_ERROR_BUFFER : AF_LLAMA_OK;
}

extern "C" int af_llama_begin(
    af_llama_engine * engine,
    const char * prompt,
    int32_t max_tokens,
    char * error,
    size_t error_capacity) {
    if (engine == nullptr || prompt == nullptr || prompt[0] == '\0' || max_tokens <= 0) {
        set_error(error, error_capacity, "prompt and max_tokens must be provided");
        return AF_LLAMA_ERROR_INVALID_ARGUMENT;
    }

    clear_generation(engine);
    engine->cancelled.store(false, std::memory_order_relaxed);

    const size_t prompt_size = std::strlen(prompt);
    const int32_t token_count = llama_tokenize(
        engine->vocab, prompt, prompt_size, nullptr, 0, true, true);
    if (token_count >= 0) {
        return fail(engine, AF_LLAMA_ERROR_TOKENIZE, "tokenizer did not report required capacity", error, error_capacity);
    }

    engine->prompt_tokens.resize(static_cast<size_t>(-token_count));
    const int32_t written = llama_tokenize(
        engine->vocab,
        prompt,
        prompt_size,
        engine->prompt_tokens.data(),
        static_cast<int32_t>(engine->prompt_tokens.size()),
        true,
        true);
    if (written < 0) {
        return fail(engine, AF_LLAMA_ERROR_TOKENIZE, "failed to tokenize prompt", error, error_capacity);
    }
    engine->prompt_tokens.resize(static_cast<size_t>(written));

    const uint64_t required = static_cast<uint64_t>(written) + static_cast<uint64_t>(max_tokens);
    if (required > engine->context_size) {
        return fail(
            engine,
            AF_LLAMA_ERROR_CONTEXT,
            "prompt tokens plus max_tokens exceed configured context size",
            error,
            error_capacity);
    }

    llama_context_params context_params = llama_context_default_params();
    context_params.n_ctx = engine->context_size;
    context_params.n_batch = std::min(
        engine->batch_size,
        static_cast<uint32_t>(std::max<int32_t>(written, 1)));
    context_params.n_threads = engine->threads;
    context_params.n_threads_batch = engine->threads;
    context_params.abort_callback = abort_decode;
    context_params.abort_callback_data = engine;
    engine->context = llama_init_from_model(engine->model, context_params);
    if (engine->context == nullptr) {
        return fail(engine, AF_LLAMA_ERROR_CONTEXT, "failed to create llama context", error, error_capacity);
    }

    llama_sampler_chain_params sampler_params = llama_sampler_chain_default_params();
    engine->sampler = llama_sampler_chain_init(sampler_params);
    if (engine->sampler == nullptr) {
        return fail(engine, AF_LLAMA_ERROR_CONTEXT, "failed to create sampler", error, error_capacity);
    }
    if (engine->temperature <= 0.0f) {
        llama_sampler_chain_add(engine->sampler, llama_sampler_init_greedy());
    } else {
        if (engine->top_k > 0) {
            llama_sampler_chain_add(engine->sampler, llama_sampler_init_top_k(engine->top_k));
        }
        if (engine->top_p > 0.0f && engine->top_p < 1.0f) {
            llama_sampler_chain_add(engine->sampler, llama_sampler_init_top_p(engine->top_p, 1));
        }
        llama_sampler_chain_add(engine->sampler, llama_sampler_init_temp(engine->temperature));
        llama_sampler_chain_add(engine->sampler, llama_sampler_init_dist(engine->seed));
    }

    engine->max_tokens = max_tokens;
    engine->prompt_pending = true;
    return AF_LLAMA_OK;
}

extern "C" int af_llama_next(
    af_llama_engine * engine,
    char * token_piece,
    size_t token_piece_capacity,
    size_t * token_piece_size,
    char * error,
    size_t error_capacity) {
    if (engine == nullptr || engine->context == nullptr || engine->sampler == nullptr ||
        token_piece == nullptr || token_piece_capacity == 0 || token_piece_size == nullptr) {
        set_error(error, error_capacity, "generation is not initialized");
        return AF_LLAMA_ERROR_INVALID_ARGUMENT;
    }
    *token_piece_size = 0;
    if (engine->cancelled.load(std::memory_order_relaxed)) {
        clear_generation(engine);
        return AF_LLAMA_CANCELLED;
    }
    if (engine->generated_tokens >= engine->max_tokens) {
        clear_generation(engine);
        return AF_LLAMA_DONE;
    }

    llama_batch batch;
    if (engine->prompt_pending) {
        while (engine->prompt_offset < engine->prompt_tokens.size()) {
            const size_t remaining = engine->prompt_tokens.size() - engine->prompt_offset;
            const int32_t chunk_size = static_cast<int32_t>(std::min<size_t>(engine->batch_size, remaining));
            batch = llama_batch_get_one(
                engine->prompt_tokens.data() + engine->prompt_offset,
                chunk_size);
            const int decode_result = llama_decode(engine->context, batch);
            if (decode_result != 0) {
                if (engine->cancelled.load(std::memory_order_relaxed)) {
                    clear_generation(engine);
                    return AF_LLAMA_CANCELLED;
                }
                return fail(
                    engine,
                    AF_LLAMA_ERROR_DECODE,
                    "prompt prefill failed with code " + std::to_string(decode_result),
                    error,
                    error_capacity);
            }
            engine->prompt_offset += static_cast<size_t>(chunk_size);
        }
        engine->prompt_pending = false;
    } else {
        batch = llama_batch_get_one(&engine->pending_token, 1);
        const int decode_result = llama_decode(engine->context, batch);
        if (decode_result != 0) {
            if (engine->cancelled.load(std::memory_order_relaxed)) {
                clear_generation(engine);
                return AF_LLAMA_CANCELLED;
            }
            return fail(
                engine,
                AF_LLAMA_ERROR_DECODE,
                "token decode failed with code " + std::to_string(decode_result),
                error,
                error_capacity);
        }
    }

    const llama_token token = llama_sampler_sample(engine->sampler, engine->context, -1);
    if (llama_vocab_is_eog(engine->vocab, token)) {
        clear_generation(engine);
        return AF_LLAMA_DONE;
    }

    const int32_t piece_size = llama_token_to_piece(
        engine->vocab,
        token,
        token_piece,
        static_cast<int32_t>(token_piece_capacity),
        0,
        true);
    if (piece_size < 0 || static_cast<size_t>(piece_size) > token_piece_capacity) {
        return fail(engine, AF_LLAMA_ERROR_BUFFER, "token piece buffer is too small", error, error_capacity);
    }

    engine->pending_token = token;
    engine->generated_tokens++;
    *token_piece_size = static_cast<size_t>(piece_size);
    return AF_LLAMA_OK;
}

extern "C" void af_llama_cancel(af_llama_engine * engine) {
    if (engine != nullptr) {
        engine->cancelled.store(true, std::memory_order_relaxed);
    }
}
