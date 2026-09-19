package module

import "github.com/mekjr1/midden/internal/confirmation"

type HostStatusInput struct {
	ProbeConfirmation bool `json:"probe_confirmation,omitempty"`
}
type HostStatus struct {
	ConfirmationAvailable bool   `json:"confirmation_available"`
	ConfirmationOutcome   string `json:"confirmation_outcome"`
	WorkflowChanged       bool   `json:"workflow_changed"`
	Diagnostic            string `json:"diagnostic,omitempty"`
}

func hostStatus(req Request, input HostStatusInput) HostStatus {
	result := HostStatus{ConfirmationAvailable: req.ConfirmOperator != nil, ConfirmationOutcome: "not_requested"}
	if !result.ConfirmationAvailable {
		result.ConfirmationOutcome = "unavailable"
		return result
	}
	if input.ProbeConfirmation {
		accepted, err := confirmedByHost(req, confirmation.Request{Action: "probe", SubjectID: "confirmation-probe",
			Digest:  DigestSHA256([]byte("confirmation-probe")),
			Message: "Midden confirmation-channel test only. Your response will not approve evidence, review a draft, publish, or change workflow state."})
		result.ConfirmationOutcome = confirmation.Outcome(accepted, err)
		if err != nil {
			result.Diagnostic = err.Error()
		}
	}
	return result
}
