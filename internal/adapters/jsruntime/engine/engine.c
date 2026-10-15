// engine.c is the JavaScript engine user code runs in: QuickJS-NG, compiled to
// WebAssembly by build.sh, with one way out of the sandbox: host calls, which Go answers.
// No std or os module is built in. Each instance runs one piece of code once and is then
// thrown away, so nothing here frees what it allocates at the end.
//
// The code is a script that defines its entry function at the top, or in an object there.
// run calls it with the input parsed from JSON, lets the promise it may return settle,
// and keeps the result as JSON; or keeps what went wrong.

#include <malloc.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "quickjs.h"

#define IMPORT(name) __attribute__((import_module("polythought"), import_name(name)))
#define EXPORT(name) __attribute__((export_name(name)))

// host_call asks the host to do name with args, both JSON text. It returns the length of
// the answer, for host_read to copy out, or -1 minus its length when the answer is an
// error message.
IMPORT("host_call") int32_t host_call(const char *name, int32_t name_len, const char *args, int32_t args_len);
IMPORT("host_read") void host_read(char *dst);
// interrupted says whether the run is out of time.
IMPORT("interrupted") int32_t interrupted(void);
// host_log hands the host a line of console output.
IMPORT("log") void host_log(int32_t level, const char *text, int32_t len);

// What run returns.
enum {
    RUN_OK = 0,            // the result is the output, as JSON
    RUN_THREW = 1,         // the result is {name, message, stack}, as JSON
    RUN_OUT_OF_MEMORY = 2, // even if the code caught it
    RUN_OUT_OF_TIME = 3,
    RUN_NO_ENTRY = 4,      // the entry is not a function
    RUN_PENDING = 5,       // it returned a promise that never settles
};

// Memory: the engine's allocations count against a limit. Refusing one makes the engine
// throw "out of memory", which code can catch; ran_out remembers it all the same.

static size_t mem_used, mem_limit;
static int ran_out;

static size_t usable(const void *ptr)
{
    return malloc_usable_size((void *)ptr);
}

static int allow(size_t more)
{
    if (more > mem_limit || mem_used > mem_limit - more) {
        ran_out = 1;
        return 0;
    }
    return 1;
}

static void *counted(void *ptr)
{
    if (ptr)
        mem_used += usable(ptr);
    else
        ran_out = 1;
    return ptr;
}

static void *js_malloc_counted(void *opaque, size_t size)
{
    return allow(size) ? counted(malloc(size)) : NULL;
}

static void *js_calloc_counted(void *opaque, size_t count, size_t size)
{
    if (size && count > SIZE_MAX / size)
        return NULL;
    return allow(count * size) ? counted(calloc(count, size)) : NULL;
}

static void js_free_counted(void *opaque, void *ptr)
{
    if (!ptr)
        return;
    mem_used -= usable(ptr);
    free(ptr);
}

static void *js_realloc_counted(void *opaque, void *ptr, size_t size)
{
    size_t old = ptr ? usable(ptr) : 0;
    void *grown;

    if (size == 0) {
        js_free_counted(opaque, ptr);
        return NULL;
    }
    if (size > old && !allow(size - old))
        return NULL;
    grown = realloc(ptr, size);
    if (!grown) {
        ran_out = 1;
        return NULL;
    }
    mem_used = mem_used - old + usable(grown);
    return grown;
}

static const JSMallocFunctions counted_malloc = {
    js_calloc_counted, js_malloc_counted, js_free_counted, js_realloc_counted, usable,
};

// Time: the engine asks every so often whether to stop. Once out of time, it throws an
// error code cannot catch.

static int out_of_time;

static int on_interrupt(JSRuntime *rt, void *opaque)
{
    if (!out_of_time && interrupted())
        out_of_time = 1;
    return out_of_time;
}

// The result, read by the host once run returns. It is outside the memory limit.

static char *result;
static int32_t result_len;

static void keep(const char *text, size_t len)
{
    result = malloc(len);
    result_len = result ? (int32_t)len : 0;
    if (result)
        memcpy(result, text, len);
}

static void keep_text(const char *text)
{
    keep(text, strlen(text));
}

static void keep_string(JSContext *ctx, JSValueConst v)
{
    size_t len;
    const char *s = JS_ToCStringLen(ctx, &len, v);

    if (s) {
        keep(s, len);
        JS_FreeCString(ctx, s);
    }
}

// copy_property copies name from one object to another, if reading it works.
static void copy_property(JSContext *ctx, JSValueConst to, JSValueConst from, const char *name)
{
    JSValue v = JS_GetPropertyStr(ctx, from, name);

    if (JS_IsException(v))
        JS_FreeValue(ctx, JS_GetException(ctx));
    else
        JS_SetPropertyStr(ctx, to, name, v);
}

// threw keeps what the code threw as the result.
static int32_t threw(JSContext *ctx)
{
    JSValue exc = JS_GetException(ctx);
    JSValue report, json;

    if (out_of_time)
        return RUN_OUT_OF_TIME;

    // Room to describe it, even when memory ran out.
    mem_limit += 1 << 20;

    report = JS_NewObject(ctx);
    if (JS_IsError(exc)) {
        copy_property(ctx, report, exc, "name");
        copy_property(ctx, report, exc, "message");
        copy_property(ctx, report, exc, "stack");
    } else {
        JSValue s = JS_ToString(ctx, exc);
        if (JS_IsException(s)) {
            JS_FreeValue(ctx, JS_GetException(ctx));
            s = JS_NewString(ctx, "a value that is not an error");
        }
        JS_SetPropertyStr(ctx, report, "message", s);
    }

    json = JS_JSONStringify(ctx, report, JS_UNDEFINED, JS_UNDEFINED);
    if (JS_IsString(json))
        keep_string(ctx, json);
    else
        keep_text("{\"message\":\"what the code threw could not be described\"}");

    return ran_out ? RUN_OUT_OF_MEMORY : RUN_THREW;
}

// __host(name, args) makes a host call: args go to Go as JSON, and the answer comes back
// parsed, or thrown as an Error when Go answers with one.
static JSValue js_host(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv)
{
    size_t name_len, args_len = 4;
    const char *name, *args = "null", *args_c = NULL;
    JSValue args_json, v;
    int32_t n;
    int failed;
    char *answer;

    name = JS_ToCStringLen(ctx, &name_len, argv[0]);
    if (!name)
        return JS_EXCEPTION;

    args_json = JS_JSONStringify(ctx, argv[1], JS_UNDEFINED, JS_UNDEFINED);
    if (JS_IsException(args_json)) {
        JS_FreeCString(ctx, name);
        return JS_EXCEPTION;
    }
    if (JS_IsString(args_json)) {
        args_c = JS_ToCStringLen(ctx, &args_len, args_json);
        if (!args_c) {
            JS_FreeCString(ctx, name);
            JS_FreeValue(ctx, args_json);
            return JS_EXCEPTION;
        }
        args = args_c;
    }

    n = host_call(name, (int32_t)name_len, args, (int32_t)args_len);

    JS_FreeCString(ctx, name);
    if (args_c)
        JS_FreeCString(ctx, args_c);
    JS_FreeValue(ctx, args_json);

    failed = n < 0;
    if (failed)
        n = -1 - n;

    // Without room for it, the answer is dropped: the host forgets it on the next call.
    answer = js_malloc(ctx, (size_t)n + 1);
    if (!answer)
        return JS_EXCEPTION;
    host_read(answer);
    answer[n] = '\0';

    if (failed)
        v = JS_ThrowPlainError(ctx, "%s", answer);
    else
        v = JS_ParseJSON(ctx, answer, (size_t)n, "<host>");
    js_free(ctx, answer);

    return v;
}

// __log(level, text) is what console writes with.
static JSValue js_log(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv)
{
    int32_t level;
    size_t len;
    const char *text;

    if (JS_ToInt32(ctx, &level, argv[0]))
        return JS_EXCEPTION;
    text = JS_ToCStringLen(ctx, &len, argv[1]);
    if (!text)
        return JS_EXCEPTION;

    host_log(level, text, (int32_t)len);
    JS_FreeCString(ctx, text);

    return JS_UNDEFINED;
}

// The prelude gives the code a console: strings as they are, other values as JSON where
// they can be.
static const char prelude[] =
    "(() => {\n"
    "  const log = globalThis.__log;\n"
    "  delete globalThis.__log;\n"
    "  const show = (v) => {\n"
    "    if (typeof v === 'string') return v;\n"
    "    try { const j = JSON.stringify(v); if (j !== undefined) return j; } catch {}\n"
    "    return String(v);\n"
    "  };\n"
    "  const line = (level) => (...args) => log(level, args.map(show).join(' '));\n"
    "  globalThis.console = { log: line(0), debug: line(0), info: line(1), warn: line(2), error: line(3) };\n"
    "})();\n";

static int add_globals(JSContext *ctx)
{
    JSValue global = JS_GetGlobalObject(ctx);
    JSValue v;

    JS_SetPropertyStr(ctx, global, "__host", JS_NewCFunction(ctx, js_host, "__host", 2));
    JS_SetPropertyStr(ctx, global, "__log", JS_NewCFunction(ctx, js_log, "__log", 2));
    JS_FreeValue(ctx, global);

    v = JS_Eval(ctx, prelude, sizeof prelude - 1, "<prelude>", JS_EVAL_TYPE_GLOBAL);
    if (JS_IsException(v))
        return -1;
    JS_FreeValue(ctx, v);

    return 0;
}

// settle runs the jobs promises queued. There are no timers or I/O, so whatever is not
// done by then never will be.
static int settle(JSContext *ctx, JSContext **failed)
{
    for (;;) {
        int r = JS_ExecutePendingJob(JS_GetRuntime(ctx), failed);
        if (r <= 0)
            return r;
    }
}

static int32_t call(JSContext *ctx, const char *source, int32_t source_len, const char *entry,
                    const char *input, int32_t input_len)
{
    JSValue v, global, fn, arg, ret, json;
    JSContext *failed;

    v = JS_Eval(ctx, source, (size_t)source_len, "code.js", JS_EVAL_TYPE_GLOBAL);
    if (JS_IsException(v))
        return threw(ctx);
    JS_FreeValue(ctx, v);

    // By name, so a const or let at the top counts too, or a path of names like
    // exports.run. The host checked it is one.
    fn = JS_Eval(ctx, entry, strlen(entry), "<entry>", JS_EVAL_TYPE_GLOBAL);
    if (JS_IsException(fn)) {
        if (out_of_time || ran_out)
            return threw(ctx);
        JS_FreeValue(ctx, JS_GetException(ctx));
        return RUN_NO_ENTRY;
    }
    if (!JS_IsFunction(ctx, fn))
        return RUN_NO_ENTRY;
    global = JS_GetGlobalObject(ctx);

    arg = JS_ParseJSON(ctx, input, (size_t)input_len, "<input>");
    if (JS_IsException(arg))
        return threw(ctx);

    ret = JS_Call(ctx, fn, global, 1, (JSValueConst *)&arg);
    if (JS_IsException(ret))
        return threw(ctx);

    if (settle(ctx, &failed) < 0)
        return threw(failed);

    if (JS_IsPromise(ret)) {
        switch (JS_PromiseState(ctx, ret)) {
        case JS_PROMISE_FULFILLED:
            v = JS_PromiseResult(ctx, ret);
            JS_FreeValue(ctx, ret);
            ret = v;
            break;
        case JS_PROMISE_REJECTED:
            JS_Throw(ctx, JS_PromiseResult(ctx, ret));
            return threw(ctx);
        default:
            return RUN_PENDING;
        }
    }

    json = JS_JSONStringify(ctx, ret, JS_UNDEFINED, JS_UNDEFINED);
    if (JS_IsException(json))
        return threw(ctx);

    // undefined, a function: nothing JSON can hold.
    if (!JS_IsString(json))
        keep_text("null");
    else
        keep_string(ctx, json);

    return RUN_OK;
}

// alloc gives the host room to write the run's arguments.
EXPORT("alloc")
void *alloc(int32_t size)
{
    return malloc((size_t)size);
}

EXPORT("result_ptr")
char *result_ptr(void)
{
    return result;
}

EXPORT("result_len")
int32_t result_length(void)
{
    return result_len;
}

// run runs source and calls its function entry with input. source, entry and input end
// with a NUL byte, past their lengths. memory_limit bounds the engine's allocations;
// stack_size the stack JavaScript may use, below the engine's own.
EXPORT("run")
int32_t run(const char *source, int32_t source_len, const char *entry, const char *input,
            int32_t input_len, uint32_t memory_limit, uint32_t stack_size)
{
    JSRuntime *rt;
    JSContext *ctx;
    int32_t status;

    mem_limit = memory_limit;

    rt = JS_NewRuntime2(&counted_malloc, NULL);
    if (!rt)
        return RUN_OUT_OF_MEMORY;
    JS_SetMaxStackSize(rt, stack_size);
    JS_SetInterruptHandler(rt, on_interrupt, NULL);

    ctx = JS_NewContext(rt);
    if (!ctx)
        return RUN_OUT_OF_MEMORY;
    if (add_globals(ctx) < 0)
        return threw(ctx);

    status = call(ctx, source, source_len, entry, input, input_len);
    if (out_of_time)
        return RUN_OUT_OF_TIME;
    if (ran_out)
        return RUN_OUT_OF_MEMORY;

    return status;
}
