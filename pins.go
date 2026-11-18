package main

// modelRepos is the trust anchor: the repository each model's enclaves must
// attest to. The gateway's catalog cannot add to or change it, so serving a
// new model takes a release.
var modelRepos = map[string]string{
	"nomic-embed-text":         "tinfoilsh/confidential-small-models",
	"doc-upload":               "tinfoilsh/confidential-doc-upload",
	"whisper-large-v3-turbo":   "tinfoilsh/confidential-small-models",
	"voxtral-small-24b":        "tinfoilsh/confidential-voxtral-small-24b",
	"voxtral-mini-4b-realtime": "tinfoilsh/confidential-small-models",
	"voxtral-tts":              "tinfoilsh/confidential-small-models",
	"llama3-3-70b":             "tinfoilsh/confidential-llama3-3-70b",
	"glm-5-3":                  "tinfoilsh/confidential-glm5-3-nvfp4",
	"inkling-2-small":          "tinfoilsh/confidential-inkling-2-small",
	"gpt-oss-120b":             "tinfoilsh/confidential-gpt-oss-120b",
	"gpt-oss-safeguard-120b":   "tinfoilsh/confidential-gpt-oss-safeguard-120b",
	"qwen3-tts":                "tinfoilsh/confidential-small-models",
	"qwen3-tts-base":           "tinfoilsh/confidential-small-models",
	"deepseek-v4-1-flash":      "tinfoilsh/confidential-deepseek-v4-1-flash",
	"kimi-k3-1-pro":            "tinfoilsh/confidential-kimi-k3-1-pro",
	"websearch":                "tinfoilsh/confidential-websearch",
	"gemma4-31b":               "tinfoilsh/confidential-gemma4-31b",
	"pii-filter":               "tinfoilsh/confidential-pii-cpu",
	"decision-large":           "tinfoilsh/confidential-decision-models",
	"decision-medium":          "tinfoilsh/confidential-decision-models",
	"decision-small":           "tinfoilsh/confidential-decision-models",
}
