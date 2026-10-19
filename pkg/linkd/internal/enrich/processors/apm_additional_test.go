package processors

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestAPMServiceFromAdditionalDimensions(t *testing.T) {
	t.Parallel()
	for _, instance := range []string{"", "instance-a"} {
		for _, placement := range []string{"dimensions", "additional", "additional without dimensions"} {
			if placement == "additional without dimensions" && instance != "" {
				continue
			}
			t.Run(placement+"/"+instance, func(t *testing.T) {
				reader := apmTestReader{}
				event := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldAPMInstanceID: domain.NewStringScalar(instance)})
				additional := `{"app_name":"demo","service_name":"checkout"}`
				if placement == "dimensions" {
					event.Dimensions[rules.FieldServiceName] = domain.NewStringScalar("checkout")
					additional = `{"app_name":"demo"}`
				}
				if placement == "additional without dimensions" {
					event.Dimensions = nil
				}
				event.ExtraData = domain.JSONObject{"additional_dimensions": json.RawMessage(additional)}
				before, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, APM{}}, enrich.Sources{CWStrategy: reader, OneModel: reader, APMApplication: reader, Metric: reader})
				if err != nil {
					t.Fatal(err)
				}
				result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
				if err != nil {
					t.Fatal(err)
				}
				payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
				if err != nil {
					t.Fatal(err)
				}
				model, id := "cw-service", "17|checkout"
				if instance != "" {
					model, id = "cw-service_instance", id+"|"+instance
				}
				for i, name := range []string{rules.StrategyProcessor, rules.ResourceProcessor, rules.DisplayProcessor, rules.APMProcessor} {
					envelope := payload.Processors[i][name]
					if envelope.Status != domain.EnrichStatusSucceeded {
						t.Fatalf("%s: %+v", name, envelope)
					}
					if name == rules.ResourceProcessor || name == rules.APMProcessor {
						if string(envelope.Value["model_id"]) != `"`+model+`"` || string(envelope.Value["model_inst_id"]) != `"`+id+`"` {
							t.Fatalf("%s: %+v", name, envelope)
						}
					}
				}
				if string(payload.Processors[2][rules.DisplayProcessor].Value["object"]) != `"checkout"` || string(payload.Processors[3][rules.APMProcessor].Value["apm_service_name"]) != `"checkout"` {
					t.Fatalf("payload=%+v", payload)
				}
				after, err := json.Marshal(event)
				if err != nil || string(before) != string(after) {
					t.Fatal("APM enrichment changed Event dimensions")
				}
			})
		}
	}
}

func TestAPMRejectsConflictingOrInvalidAdditionalDimensions(t *testing.T) {
	for _, raw := range []string{`{"service_name":"other"}`, `{"service_name":{}}`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			reader := apmTestReader{}
			event := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldServiceName: domain.NewStringScalar("checkout")})
			event.ExtraData = domain.JSONObject{"additional_dimensions": json.RawMessage(raw)}
			chain, err := enrich.NewChain([]enrich.Processor{Resource{}, Display{}, APM{}}, enrich.Sources{CWStrategy: reader, OneModel: reader, APMApplication: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			for i, name := range []string{rules.ResourceProcessor, rules.DisplayProcessor, rules.APMProcessor} {
				envelope := payload.Processors[i][name]
				if envelope.Status != domain.EnrichStatusFailed || len(envelope.Diagnostics) == 0 || envelope.Diagnostics[0].Code != enrich.DiagnosticCodeInvalidField {
					t.Fatalf("%s: %+v", name, envelope)
				}
			}
		})
	}
}

func TestAPMDisplayObjectUsesAdditionalService(t *testing.T) {
	event := processorBaseTargetAlert(t, nil)
	event.ExtraData = domain.JSONObject{"additional_dimensions": json.RawMessage(`{"app_name":"demo","service_name":"checkout"}`)}
	strategy := models.CWStrategy{Spec: models.CWStrategySpec{TableID: "2_bkapm_metric_demo.__default__"}}
	if got := cleanDisplayObject(event, strategy, models.ResourceValues{}); got != "checkout" {
		t.Fatalf("display object=%q", got)
	}
}
