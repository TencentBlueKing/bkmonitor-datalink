package observability

// StageAuthStore is the CLI's authorization store not answering: one line
// per failure, limited like any other, carrying the error's own bounded
// text -- which the public authorization answer leaves out.
const StageAuthStore = "auth_store"
