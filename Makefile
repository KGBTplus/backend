generate:
	# Генерируем код сервера из OpenAPI
	oapi-codegen -config api/config.yaml api/openapi.yaml > internal/api/api.gen.go
	# Генерируем код для БД из SQL
	sqlc generate
