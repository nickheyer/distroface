import { rpcClient } from '$lib/api/rpc-client';
import { toJson, type JsonValue } from '@bufbuild/protobuf';
import { ValueSchema, type Value } from '@bufbuild/protobuf/wkt';

class ConfigStore {
	entries = $state<Record<string, JsonValue>>({});

	async init() {
		try {
			const resp = await rpcClient.configuration.getConfiguration({});
			for (const entry of resp.entries) { // ConfigEntry[]
				if (entry.value) {
					this.store(entry.key, entry.value); // Store as union type
				}
			}
		} catch {
			// nada on failure
		}
	}

	store(key: string, value: Value) {
		this.entries[key] = toJson(ValueSchema, value);
	}

	get<T>(key: string, fallback?: T): JsonValue | T {
		return this.entries[key] ?? fallback as T;
	}
}



export const configStore = new ConfigStore();
