# PROTOTYPE for #155. AppRole login from a volume only this service mounts; token injected into LiteLLM's env.
vault {
  address = "http://openbao:8200"
}

auto_auth {
  method "approle" {
    config = {
      role_id_file_path                   = "/approle/role_id"
      secret_id_file_path                 = "/approle/secret_id"
      remove_secret_id_file_after_reading = false
    }
  }
}

template_config {
  static_secret_render_interval = "10s"
  exit_on_retry_failure         = true
}

env_template "CLAUDE_OAUTH_TOKEN" {
  contents             = "{{ with secret \"satat/data/claude-oauth-token\" }}{{ .Data.data.value }}{{ end }}"
  error_on_missing_key = true
}

# Same token for LiteLLM's raw /anthropic pass-through route.
env_template "ANTHROPIC_API_KEY" {
  contents             = "{{ with secret \"satat/data/claude-oauth-token\" }}{{ .Data.data.value }}{{ end }}"
  error_on_missing_key = true
}

exec {
  command                   = ["docker/prod_entrypoint.sh", "--config", "/app/config.yaml", "--port", "4000"]
  restart_on_secret_changes = "always"
  restart_stop_signal       = "SIGTERM"
}
