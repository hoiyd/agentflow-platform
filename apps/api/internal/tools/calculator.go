package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

func CalculatorTool() Binding {
	return Binding{
		Descriptor: Descriptor{
			Name:        "calculator",
			Description: "Evaluate a basic arithmetic expression with +, -, *, /, and parentheses.",
			Concurrency: ConcurrencyPolicy{Mode: ConcurrencyReadOnly},
			Parameters: ObjectSchema(map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "Arithmetic expression to evaluate, for example: (128 * 37) / 4",
				},
			}, []string{"expression"}),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid calculator arguments: %w", err)
			}
			value, err := evalExpression(input.Expression)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"expression": input.Expression,
				"value":      value,
			}, nil
		},
	}
}

type expressionParser struct {
	input []rune
	pos   int
}

func evalExpression(input string) (float64, error) {
	if strings.TrimSpace(input) == "" {
		return 0, errors.New("expression is required")
	}
	parser := &expressionParser{input: []rune(input)}
	value, err := parser.parseExpression()
	if err != nil {
		return 0, err
	}
	parser.skipSpaces()
	if parser.pos != len(parser.input) {
		return 0, fmt.Errorf("unexpected token %q", parser.input[parser.pos])
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, errors.New("expression result is not finite")
	}
	return value, nil
}

func (p *expressionParser) parseExpression() (float64, error) {
	value, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.match('+') {
			next, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			value += next
			continue
		}
		if p.match('-') {
			next, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			value -= next
			continue
		}
		return value, nil
	}
}

func (p *expressionParser) parseTerm() (float64, error) {
	value, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.match('*') {
			next, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			value *= next
			continue
		}
		if p.match('/') {
			next, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			if next == 0 {
				return 0, errors.New("division by zero")
			}
			value /= next
			continue
		}
		return value, nil
	}
}

func (p *expressionParser) parseFactor() (float64, error) {
	p.skipSpaces()
	if p.match('+') {
		return p.parseFactor()
	}
	if p.match('-') {
		value, err := p.parseFactor()
		return -value, err
	}
	if p.match('(') {
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if !p.match(')') {
			return 0, errors.New("missing closing parenthesis")
		}
		return value, nil
	}
	return p.parseNumber()
}

func (p *expressionParser) parseNumber() (float64, error) {
	p.skipSpaces()
	start := p.pos
	for p.pos < len(p.input) && (unicode.IsDigit(p.input[p.pos]) || p.input[p.pos] == '.') {
		p.pos++
	}
	if start == p.pos {
		return 0, errors.New("number expected")
	}
	value, err := strconv.ParseFloat(string(p.input[start:p.pos]), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}
	return value, nil
}

func (p *expressionParser) skipSpaces() {
	for p.pos < len(p.input) && unicode.IsSpace(p.input[p.pos]) {
		p.pos++
	}
}

func (p *expressionParser) match(expected rune) bool {
	if p.pos >= len(p.input) || p.input[p.pos] != expected {
		return false
	}
	p.pos++
	return true
}
