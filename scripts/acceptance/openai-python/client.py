import json
import os
import time
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime

from openai import APIStatusError, OpenAI


def required_environment(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeError(f"{name} is required")
    return value


def record(name, action, expected_error=False):
    started = time.monotonic()
    result = {"name": name, "succeeded": False}
    explicit_reissue = os.environ.get("LLMGATEWAY_SDK_EXPLICIT_REISSUE", "").lower() == "true"
    for attempt in range(1, 5):
        try:
            action()
            result["succeeded"] = not expected_error
            break
        except APIStatusError as error:
            result["httpStatus"] = error.status_code
            body = error.body if isinstance(error.body, dict) else {}
            nested = body.get("error", body) if isinstance(body, dict) else {}
            result["errorCode"] = nested.get("code", "") if isinstance(nested, dict) else ""
            result["errorType"] = nested.get("type", "") if isinstance(nested, dict) else ""
            if explicit_reissue and not expected_error and attempt < 4:
                delay = gateway_retry_delay(error, result["errorCode"])
                if delay is not None:
                    time.sleep(delay)
                    continue
            result["succeeded"] = (
                expected_error
                and result["httpStatus"] == 429
                and result["errorCode"] == "1113"
                and result["errorType"] == "quota"
            )
            break
        except Exception as error:
            result["errorType"] = type(error).__name__
            result["succeeded"] = False
            break
    result["latencyMillis"] = max(int((time.monotonic() - started) * 1000), 0)
    return result


def gateway_retry_delay(error: APIStatusError, code: str):
    retryable_codes = {
        "upstream_outcome_uncertain",
        "upstream_circuit_open",
        "free_pool_unavailable",
        "503",
    }
    if error.status_code not in {429, 503} and code not in retryable_codes:
        return None
    retry_after = error.response.headers.get("retry-after")
    if not retry_after:
        return None
    try:
        return max(float(retry_after) + 0.25, 0.25)
    except ValueError:
        try:
            deadline = parsedate_to_datetime(retry_after)
            if deadline.tzinfo is None:
                deadline = deadline.replace(tzinfo=timezone.utc)
            return max((deadline - datetime.now(timezone.utc)).total_seconds() + 1.0, 0.25)
        except (TypeError, ValueError):
            return None


base_url = required_environment("LLMGATEWAY_SDK_BASE_URL").rstrip("/") + "/"
api_key = required_environment("LLMGATEWAY_SDK_API_KEY")
success_model = required_environment("LLMGATEWAY_SDK_SUCCESS_MODEL")
stream_model = required_environment("LLMGATEWAY_SDK_STREAM_MODEL")
reasoning_mode = required_environment("LLMGATEWAY_SDK_REASONING_MODE")
error_model = required_environment("LLMGATEWAY_SDK_ERROR_MODEL")
client = OpenAI(base_url=base_url, api_key=api_key, max_retries=0, timeout=150.0)


def models():
    listing = client.models.list()
    model_ids = {model.id for model in listing.data}
    if success_model not in model_ids or stream_model not in model_ids:
        raise RuntimeError("an acceptance model is absent from the authorized catalog")


def chat():
    completion = client.chat.completions.create(
        model=success_model,
        messages=[{"role": "user", "content": "Reply with exactly OK."}],
        max_tokens=256,
    )
    if not completion.id or not completion.choices or not completion.choices[0].message.content:
        raise RuntimeError("chat completion is incomplete")
    if not completion.usage or completion.usage.total_tokens <= 0:
        raise RuntimeError("chat completion usage is missing")


def chat_stream():
    stream = client.chat.completions.create(
        model=stream_model,
        messages=[{"role": "user", "content": "Reply with exactly OK."}],
        max_tokens=256,
        stream=True,
    )
    chunks = 0
    content = ""
    for chunk in stream:
        chunks += 1
        for choice in chunk.choices:
            content += choice.delta.content or ""
    if chunks == 0 or not content:
        raise RuntimeError("chat stream is incomplete")


def responses():
    response = client.responses.create(
        model=success_model,
        input="Reply with exactly OK.",
        max_output_tokens=256,
    )
    if not response.id or not response.output_text or not response.usage or response.usage.total_tokens <= 0:
        raise RuntimeError("Responses output is incomplete")


def tools():
    completion = client.chat.completions.create(
        model=success_model,
        messages=[{"role": "user", "content": "Call the lookup tool for Beijing. Do not answer directly."}],
        tools=[
            {
                "type": "function",
                "function": {
                    "name": "lookup",
                    "description": "Look up a city",
                    "parameters": {
                        "type": "object",
                        "properties": {"city": {"type": "string"}},
                        "required": ["city"],
                    },
                },
            }
        ],
        tool_choice="auto",
        max_tokens=256,
    )
    if not completion.choices or not completion.choices[0].message.tool_calls:
        raise RuntimeError("tool completion is missing the forced function call")
    if completion.choices[0].message.tool_calls[0].function.name != "lookup":
        raise RuntimeError("tool completion returned the wrong function")


def reasoning():
    arguments = {
        "model": success_model,
        "messages": [{"role": "user", "content": "Reply with exactly OK after reasoning."}],
        "max_tokens": 256,
    }
    if reasoning_mode == "toggle":
        arguments["extra_body"] = {"thinking": {"type": "enabled"}}
    elif reasoning_mode in {"effort", "hybrid"}:
        arguments["reasoning_effort"] = "low"
    else:
        raise RuntimeError("unsupported reasoning mode")
    completion = client.chat.completions.create(**arguments)
    if not completion.choices or not completion.usage or completion.usage.total_tokens <= 0:
        raise RuntimeError("reasoning completion is missing output or usage")


def provider_error():
    client.chat.completions.create(
        model=error_model,
        messages=[{"role": "user", "content": "Reply with exactly OK."}],
        max_tokens=16,
    )


scenarios = [
    record("models", models),
    record("chat", chat),
    record("chat_stream", chat_stream),
    record("responses", responses),
    record("tools", tools),
    record("reasoning", reasoning),
    record("provider_error", provider_error, expected_error=True),
]
summary = {
    "sdk": "openai",
    "version": "2.46.0",
    "succeeded": all(item["succeeded"] for item in scenarios),
    "scenarios": scenarios,
}
print(json.dumps(summary, separators=(",", ":"), ensure_ascii=True))
os.environ.pop("LLMGATEWAY_SDK_API_KEY", None)
raise SystemExit(0 if summary["succeeded"] else 1)
