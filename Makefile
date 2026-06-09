generate:
	# Генерирация API (сервер)
	oapi-codegen -config internal/api/oapi-codegen.yaml api/openapi.yaml
	# Генерирация кода БД
	sqlc generate
