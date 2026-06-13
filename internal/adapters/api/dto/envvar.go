package dto

// SetEnvVarsRequest replaces the full set of environment variables for a
// service (F-MVP-04). The submitted list is authoritative: any key not present
// is removed.
type SetEnvVarsRequest struct {
	Vars []EnvVarInput `json:"vars"`
}

// EnvVarInput is a single environment variable in a SetEnvVarsRequest.
type EnvVarInput struct {
	Key      string `json:"key"       binding:"required" example:"DATABASE_URL"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret" example:"false"`
}

// EnvVarDTO is the representation returned to clients. Secret values are masked
// (empty string) and never echoed back, even to operators.
type EnvVarDTO struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// EnvVarsResponse wraps the environment variables of a service.
type EnvVarsResponse struct {
	Vars  []EnvVarDTO `json:"vars"`
	Count int         `json:"count"`
}
