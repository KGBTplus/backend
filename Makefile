generate:
	# Сначала генерируем API (сервер)
	oapi-codegen -config internal/api/oapi-codegen.yaml api/openapi.yaml
	# Затем генерируем код БД
	sqlc generate
