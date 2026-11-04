# Connectors

A connector is an inference endpoint the server speaks to on behalf of agents. It holds the base url, the credential, the models agents may use and the limits for that endpoint. Agents never see the credential, they pick a connector and a model.

## Kinds

| Kind | Enum | Default base url | Credential |
| --- | --- | --- | --- |
| openai | `AGENT_PROVIDER_KIND_OPENAI` | `https://api.openai.com/v1` | `Authorization: Bearer` header |
| anthropic | `AGENT_PROVIDER_KIND_ANTHROPIC` | `https://api.anthropic.com` | `x-api-key` header, api version `2023-06-01` |
| azure_openai | `AGENT_PROVIDER_KIND_AZURE_OPENAI` | none, required | `api-key` header |
| ollama | `AGENT_PROVIDER_KIND_OLLAMA` | `http://localhost:11434` | optional `Authorization: Bearer` header |
| gemini | `AGENT_PROVIDER_KIND_GEMINI` | `https://generativelanguage.googleapis.com/v1beta/openai` | `Authorization: Bearer` header |
| bedrock | `AGENT_PROVIDER_KIND_BEDROCK` | `https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1` | `Authorization: Bearer` header |

The openai kind speaks the chat completions protocol and works with any compatible server such as vLLM, llama.cpp, LM Studio, LiteLLM or OpenRouter. Gemini and Bedrock use the OpenAI compatible endpoints of those services, so they behave like the openai kind with a different default url. Azure calls `/openai/deployments/<model>/chat/completions` on the resource url, so the model is the deployment name. Ollama speaks its native `/api/chat` protocol. Model catalogs come from `/models`, `/v1/models`, `/openai/deployments` or `/api/tags` depending on the kind.

## Fields

| Field | Description |
| --- | --- |
| name | Label shown to admins and agents, up to 100 characters. File agents reference connectors by this name. |
| kind | One of the kinds above. |
| base_url | http or https url of the endpoint. Empty uses the kind default, Azure has none. |
| api_key | Credential. Absent on update keeps the stored key, empty clears it. |
| headers | Extra request headers, applied when `set_headers` is true. Empty with `set_headers` clears them. |
| api_version | Azure `api-version` query value, `2024-10-21` when empty. Unused by other kinds. |
| default_model | Model used when an agent names none. |
| models | Models agents may pick. Empty allows any the endpoint serves. The default model is added to the list when it is missing. |
| max_tokens | Output cap per completion. Zero uses the endpoint default. |
| context_window | Context size in tokens used to compact long transcripts. Zero reads it from the model catalog. |
| timeout_seconds | Per request timeout. Zero waits as long as the model needs. |
| skip_tls_verify | Skip certificate verification for this endpoint. |
| tools_supported | The endpoint supports native tool calling. Off wraps every request in a prompted protocol where the model answers with fenced `tool` blocks. |
| requests_per_minute | Sliding window cap on requests. Zero is unlimited. |
| tokens_per_day | Prompt plus output tokens per UTC day. Sessions refuse to start, and stop, once it is spent. Zero is unlimited. |
| enabled | Disabled connectors are skipped when resolving. |
| is_default | Agents without a connector use the default of the nearest scope. The first connector of a scope becomes the default. |

## Secrets

The api key and the extra headers are sealed with the server's secret sealer before they are stored. Reads return only `api_key_set` and `headers_set`, the values never leave the server. When a stored key can no longer be decrypted the connector reports it and you re-enter the key.

## Scopes and resolution

Connectors live at system, org or repo scope. A repository may use connectors at its own scope, at its org and at the instance. Repository connectors are ignored when `git.agents.allow_repo_providers` is off, see [Agents](overview.md).

When a session starts the server picks the agent's connector if it names one and that connector is visible and enabled. Otherwise it takes the enabled connectors visible to the repository, prefers repo over org over system, and inside a scope prefers the one marked default. The model is the session override, then the agent model, then the connector default model. A model outside the allowlist is refused. A connector that was deleted falls back to the scope default on the next run.

## Private networks

Every dial checks the resolved address. Loopback, private and link local destinations are refused unless `git.agents.allow_private_networks` is on. The setting is instance only and defaults to on, so a local Ollama works out of the box. Turn it off on instances where repository admins may register connectors and the server sits next to internal services.

## Adding a connector

Under Admin > Agents, Org > Agents or Repo > Settings > Agents open Connectors and choose New. The same form is `AgentsService.CreateAgentProvider`.

```bash
curl -X POST https://forge.example.com/distroface.v1.AgentsService/CreateAgentProvider \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "scope": {"kind": "org", "id": "acme"},
    "spec": {
      "name": "ollama-lab",
      "kind": "AGENT_PROVIDER_KIND_OLLAMA",
      "baseUrl": "http://ollama.lab.internal:11434",
      "defaultModel": "qwen2.5-coder:32b",
      "models": ["qwen2.5-coder:32b", "llama3.1:70b"],
      "toolsSupported": true,
      "requestsPerMinute": 30,
      "enabled": true
    }
  }'
```

`AgentsService.ListAgentProviders` lists the connectors of a scope. Set `includeInherited` to also see the ones the scope inherits from above, they come back with `inherited` set and are edited at their own scope. `AgentsService.ListAgentProviderModels` returns the catalog of a saved connector. `AgentsService.DeleteAgentProvider` removes a connector, agents bound to it fall back to the scope default.

## Testing a connector

`AgentsService.TestAgentProvider` lists the models and sends a short completion that asks the model to call a `ping` tool. It accepts a saved connector id, an unsaved spec, or an id plus the unsaved edits of a form. The response carries `ok`, a message, the latency, the models found, the reply and `toolsOk`. When `tools_supported` is on but the model did not call the tool the message suggests turning native tools off. Saved connectors record `last_tested_at` and `last_test_error`. The Test button on the connector form calls the same RPC.

```bash
curl -X POST https://forge.example.com/distroface.v1.AgentsService/TestAgentProvider \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"scope": {"kind": "org", "id": "acme"}, "id": "<connector id>", "model": "llama3.1:70b"}'
```

## Usage

Each connector reports `tokens_today`, the tokens billed since midnight UTC. `AgentsService.GetAgentUsage` breaks sessions and tokens down by day, agent, repository, connector and model for a scope over the last thirty days by default, shown under the Usage page of each scope.
