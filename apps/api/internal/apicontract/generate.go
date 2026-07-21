// Package apicontract contains boundary DTOs generated from the shared OpenAPI contract.
package apicontract

//go:generate go -C ../../tools tool oapi-codegen -generate types -package apicontract -o ../internal/apicontract/types.gen.go ../../../api/openapi.yaml
