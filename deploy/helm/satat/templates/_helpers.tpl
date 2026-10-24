{{/* Labels on every object; .name is the component. */}}
{{- define "satat.labels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/part-of: satat
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end }}

{{/* The selector for one component's pods. */}}
{{- define "satat.selector" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end }}
