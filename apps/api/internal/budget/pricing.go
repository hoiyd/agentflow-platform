package budget

import (
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func (t *Tracker) modelPricing(estimate ModelCallEstimate) (domain.ModelRoutePricing, error) {
	if estimate.RouteID != "" {
		if t.run.RuntimeSnapshot != nil {
			for _, route := range t.run.RuntimeSnapshot.ModelRouting.Routes {
				if route.ID != estimate.RouteID {
					continue
				}
				if route.Model != strings.TrimSpace(estimate.Model) {
					return domain.ModelRoutePricing{}, fmt.Errorf("budget model does not match frozen route")
				}
				p := route.Pricing
				if p.InputPerMillionTokensMicros > 0 || p.OutputPerMillionTokensMicros > 0 || p.CachedInputPerMillionTokensMicros != nil {
					return p.Clone(), nil
				}
				// Old route metadata with absent/zero rates is not proof of free use.
				return t.globalPricing(), nil
			}
		}
		return domain.ModelRoutePricing{}, fmt.Errorf("budget model route is absent from frozen snapshot")
	}
	return t.globalPricing(), nil
}

func (t *Tracker) globalPricing() domain.ModelRoutePricing {
	return domain.ModelRoutePricing{Source: "run_budget", InputPerMillionTokensMicros: t.budget.InputCostPerMillionTokensMicros, OutputPerMillionTokensMicros: t.budget.OutputCostPerMillionTokensMicros}
}

func pricedLimits(limits domain.RuntimeRunBudget, price domain.ModelRoutePricing) domain.RuntimeRunBudget {
	limits.InputCostPerMillionTokensMicros = price.InputPerMillionTokensMicros
	limits.OutputCostPerMillionTokensMicros = price.OutputPerMillionTokensMicros
	return limits
}

func usageCost(price domain.ModelRoutePricing, prompt, completion int, breakdown *domain.UsageBreakdown, estimated bool) (int64, *domain.UsageCostDetails) {
	details := &domain.UsageCostDetails{Status: "estimated", Reason: "cache_usage_unknown", Pricing: price.Clone()}
	if price.InputPerMillionTokensMicros == 0 && price.OutputPerMillionTokensMicros == 0 && price.CachedInputPerMillionTokensMicros == nil {
		details.Status, details.Reason = "unknown", "pricing_unknown"
	}
	cached := 0
	if details.Status == "unknown" {
		return 0, details
	} else if estimated {
		details.Reason = "estimated_usage"
	} else if breakdown != nil && breakdown.CachedInputTokens != nil {
		if price.CachedInputPerMillionTokensMicros == nil {
			details.Reason = "cache_price_unknown"
		} else {
			cached = *breakdown.CachedInputTokens
			details.CacheDiscountApplied = cached > 0
			details.Reason = "provider_usage"
		}
	}
	cost := addCost(tokenCostMicros(prompt-cached, price.InputPerMillionTokensMicros), tokenCostMicros(completion, price.OutputPerMillionTokensMicros))
	if cached > 0 {
		cost = addCost(cost, tokenCostMicros(cached, *price.CachedInputPerMillionTokensMicros))
	}
	return cost, details
}
