// Copyright 2019 DeepMap, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package codegen

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/labstack/echo/v4"
)

const (
	// These allow the case statements to be sorted later:
	prefixMostSpecific, prefixLessSpecific, prefixLeastSpecific = "3", "6", "9"
	responseTypeSuffix                                          = "Response"
)

var (
	contentTypesJSON = []string{echo.MIMEApplicationJSON, "text/x-json"}
	contentTypesYAML = []string{"application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml"}
	contentTypesXML  = []string{echo.MIMEApplicationXML, echo.MIMETextXML}
)

// This function takes an array of Parameter definition, and generates a valid
// Go parameter declaration from them, eg:
// ", foo int, bar string, baz float32". The preceding comma is there to save
// a lot of work in the template engine.
func genParamArgs(params []ParameterDefinition) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		paramName := p.GoVariableName()
		parts[i] = fmt.Sprintf("%s %s", paramName, p.TypeDef())
	}
	return ", " + strings.Join(parts, ", ")
}

// This function is much like the one above, except it only produces the
// types of the parameters for a type declaration. It would produce this
// from the same input as above:
// ", int, string, float32".
func genParamTypes(params []ParameterDefinition) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.TypeDef()
	}
	return ", " + strings.Join(parts, ", ")
}

// This is another variation of the function above which generates only the
// parameter names:
// ", foo, bar, baz"
func genParamNames(params []ParameterDefinition) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.GoVariableName()
	}
	return ", " + strings.Join(parts, ", ")
}

// genResponsePayload generates the payload returned at the end of each client request function
func genResponsePayload(operationID string) string {
	var buffer = bytes.NewBufferString("")

	// Here is where we build up a response:
	fmt.Fprintf(buffer, "&%s{\n", genResponseTypeName(operationID))
	fmt.Fprintf(buffer, "Body: bodyBytes,\n")
	fmt.Fprintf(buffer, "HTTPResponse: rsp,\n")
	fmt.Fprintf(buffer, "}")

	return buffer.String()
}

// genResponseUnmarshal generates unmarshaling steps for structured response payloads
func genResponseUnmarshal(op *OperationDefinition) string {
	var handledCaseClauses = make(map[string]string)
	var unhandledCaseClauses = make(map[string]string)

	// Get the type definitions from the operation:
	typeDefinitions, err := op.GetResponseTypeDefinitions()
	if err != nil {
		panic(err)
	}

	if len(typeDefinitions) == 0 {
		// No types.
		return ""
	}

	// Add a case for each possible response:
	buffer := new(bytes.Buffer)
	responses := op.Spec.Responses
	for _, typeDefinition := range typeDefinitions {

		responseRef, ok := responses[typeDefinition.ResponseName]
		if !ok {
			continue
		}

		// We can't do much without a value:
		if responseRef.Value == nil {
			fmt.Fprintf(os.Stderr, "Response %s.%s has nil value\n", op.OperationId, typeDefinition.ResponseName)
			continue
		}

		// If there is no content-type then we have no unmarshaling to do:
		if len(responseRef.Value.Content) == 0 {
			caseAction := "break // No content-type"
			caseClauseKey := "case " + getConditionOfResponseName("rsp.StatusCode", typeDefinition.ResponseName) + ":"
			unhandledCaseClauses[prefixLeastSpecific+caseClauseKey] = fmt.Sprintf("%s\n%s\n", caseClauseKey, caseAction)
			continue
		}

		// If we made it this far then we need to handle unmarshaling for each content-type:
		sortedContentKeys := SortedContentKeys(responseRef.Value.Content)
		for _, contentTypeName := range sortedContentKeys {

			// We get "interface{}" when using "anyOf" or "oneOf" (which doesn't work with Go types):
			if typeDefinition.TypeName == "interface{}" {
				// Unable to unmarshal this, so we leave it out:
				continue
			}

			// Add content-types here (json / yaml / xml etc):
			switch {

			// JSON:
			case StringInArray(contentTypeName, contentTypesJSON):
				if typeDefinition.ContentTypeName == contentTypeName {
					var caseAction string

					caseAction = fmt.Sprintf("var dest %s\n"+
						"if err := json.Unmarshal(bodyBytes, &dest); err != nil { \n"+
						" return nil, err \n"+
						"}\n"+
						"response.%s = &dest",
						typeDefinition.Schema.TypeDecl(),
						typeDefinition.TypeName)

					caseKey, caseClause := buildUnmarshalCase(typeDefinition, caseAction, "json")
					handledCaseClauses[caseKey] = caseClause
				}

			// YAML:
			case StringInArray(contentTypeName, contentTypesYAML):
				if typeDefinition.ContentTypeName == contentTypeName {
					var caseAction string
					caseAction = fmt.Sprintf("var dest %s\n"+
						"if err := yaml.Unmarshal(bodyBytes, &dest); err != nil { \n"+
						" return nil, err \n"+
						"}\n"+
						"response.%s = &dest",
						typeDefinition.Schema.TypeDecl(),
						typeDefinition.TypeName)
					caseKey, caseClause := buildUnmarshalCase(typeDefinition, caseAction, "yaml")
					handledCaseClauses[caseKey] = caseClause
				}

			// XML:
			case StringInArray(contentTypeName, contentTypesXML):
				if typeDefinition.ContentTypeName == contentTypeName {
					var caseAction string
					caseAction = fmt.Sprintf("var dest %s\n"+
						"if err := xml.Unmarshal(bodyBytes, &dest); err != nil { \n"+
						" return nil, err \n"+
						"}\n"+
						"response.%s = &dest",
						typeDefinition.Schema.TypeDecl(),
						typeDefinition.TypeName)
					caseKey, caseClause := buildUnmarshalCase(typeDefinition, caseAction, "xml")
					handledCaseClauses[caseKey] = caseClause
				}

			// Everything else:
			default:
				caseAction := fmt.Sprintf("// Content-type (%s) unsupported", contentTypeName)
				caseClauseKey := "case " + getConditionOfResponseName("rsp.StatusCode", typeDefinition.ResponseName) + ":"
				unhandledCaseClauses[prefixLeastSpecific+caseClauseKey] = fmt.Sprintf("%s\n%s\n", caseClauseKey, caseAction)
			}
		}
	}

	if len(handledCaseClauses)+len(unhandledCaseClauses) == 0 {
		// switch would be empty.
		return ""
	}

	// Now build the switch statement in order of most-to-least specific:
	// See: https://github.com/deepmap/oapi-codegen/issues/127 for why we handle this in two separate
	// groups.
	fmt.Fprintf(buffer, "switch {\n")
	for _, caseClauseKey := range SortedStringKeys(handledCaseClauses) {

		fmt.Fprintf(buffer, "%s\n", handledCaseClauses[caseClauseKey])
	}
	for _, caseClauseKey := range SortedStringKeys(unhandledCaseClauses) {

		fmt.Fprintf(buffer, "%s\n", unhandledCaseClauses[caseClauseKey])
	}
	fmt.Fprintf(buffer, "}\n")

	return buffer.String()
}

// buildUnmarshalCase builds an unmarshalling case clause for different content-types:
func buildUnmarshalCase(typeDefinition ResponseTypeDefinition, caseAction string, contentType string) (caseKey string, caseClause string) {
	caseKey = fmt.Sprintf("%s.%s.%s", prefixLeastSpecific, contentType, typeDefinition.ResponseName)
	caseClauseKey := getConditionOfResponseName("rsp.StatusCode", typeDefinition.ResponseName)
	caseClause = fmt.Sprintf("case strings.Contains(rsp.Header.Get(\"%s\"), \"%s\") && %s:\n%s\n", echo.HeaderContentType, contentType, caseClauseKey, caseAction)
	return caseKey, caseClause
}

// genResponseTypeName creates the name of generated response types (given the operationID):
func genResponseTypeName(operationID string) string {
	return fmt.Sprintf("%s%s", UppercaseFirstCharacter(operationID), responseTypeSuffix)
}

func getResponseTypeDefinitions(op *OperationDefinition) []ResponseTypeDefinition {
	td, err := op.GetResponseTypeDefinitions()
	if err != nil {
		panic(err)
	}
	return td
}

// Return the statusCode comparison clause from the response name.
func getConditionOfResponseName(statusCodeVar, responseName string) string {
	switch responseName {
	case "default":
		return "true"
	case "1XX", "2XX", "3XX", "4XX", "5XX":
		return fmt.Sprintf("%s / 100 == %s", statusCodeVar, responseName[:1])
	default:
		return fmt.Sprintf("%s == %s", statusCodeVar, responseName)
	}
}

// getJSONResponseTypeDefinitions returns only ResponseTypeDefinitions with JSON content type
func getJSONResponseTypeDefinitions(op *OperationDefinition) []ResponseTypeDefinition {
	var jsonTds []ResponseTypeDefinition
	tds, err := op.GetResponseTypeDefinitions()
	if err != nil {
		panic(err)
	}
	for _, td := range tds {
		if  StringInArray(td.ContentTypeName, contentTypesJSON) {
			jsonTds = append(jsonTds, td)
		}
	}
	return jsonTds
}

// get2xxResponseTypeDefinition returns JSON response type definition with 2xx status code.
// returns nil if there is multiple 2xx responses.
func get2xxResponseTypeDefinition(op *OperationDefinition) *ResponseTypeDefinition {
	var tds2xx ResponseTypeDefinition
	tds := getJSONResponseTypeDefinitions(op)
	for _, td := range tds {
		if strings.HasPrefix(td.ResponseName, "2") {
			if StringInArray(td.ContentTypeName, contentTypesJSON) {
				if tds2xx.ResponseName != "" {
					return nil
				}
				tds2xx = td
			}
		}
	}

	if tds2xx.ResponseName == "" || tds2xx.Schema.TypeDecl() == "interface{}" {
		return nil
	}
	return &tds2xx
}

// hasEmpty2xxResponse checks if there is an empty 2xx response (mostly 204)
func hasEmpty2xxResponse(op *OperationDefinition) bool {
	for n, r := range op.Spec.Responses {
		if r.Value != nil && r.Value.Content == nil && strings.HasPrefix(n, "2") {
			return true
		}
	}
	return false
}

// hasSingle2xxJSONResponse checks if operation has only one JSON response with 2xx status code
func hasSingle2xxJSONResponse(op *OperationDefinition) bool {
	return get2xxResponseTypeDefinition(op) != nil
}

// hasValidOrNoBody checks if operation has body with definition or
func hasValidOrNoBody(op *OperationDefinition) bool {
	return (op.HasBody() && len(op.Bodies) > 0) || !op.HasBody()
}

// hasValidRequestAndResponse  checks if operation has valid or no body and has single JSON response or an empty response
func hasValidRequestAndResponse(op *OperationDefinition) bool {
	return hasValidOrNoBody(op) && (hasEmpty2xxResponse(op) || hasSingle2xxJSONResponse(op))
}



// This outputs a string array
func toStringArray(sarr []string) string {
	return `[]string{"` + strings.Join(sarr, `","`) + `"}`
}

func stripNewLines(s string) string {
	r := strings.NewReplacer("\n", "")
	return r.Replace(s)
}

// This function map is passed to the template engine, and we can call each
// function here by keyName from the template code.
var TemplateFunctions = template.FuncMap{
	"genParamArgs":                 genParamArgs,
	"genParamTypes":                genParamTypes,
	"genParamNames":                genParamNames,
	"genParamFmtString":            ReplacePathParamsWithStr,
	"swaggerUriToEchoUri":          SwaggerUriToEchoUri,
	"swaggerUriToChiUri":           SwaggerUriToChiUri,
	"swaggerUriToGinUri":           SwaggerUriToGinUri,
	"lcFirst":                      LowercaseFirstCharacter,
	"ucFirst":                      UppercaseFirstCharacter,
	"camelCase":                    ToCamelCase,
	"genResponsePayload":           genResponsePayload,
	"genResponseTypeName":          genResponseTypeName,
	"genResponseUnmarshal":         genResponseUnmarshal,
	"getResponseTypeDefinitions":   getResponseTypeDefinitions,
	"toStringArray":                toStringArray,
	"lower":                        strings.ToLower,
	"title":                        strings.Title,
	"stripNewLines":                stripNewLines,
	"sanitizeGoIdentity":           SanitizeGoIdentity,
	"get2xxResponseTypeDefinition": get2xxResponseTypeDefinition,
	"hasSingle2xxJSONResponse": 	hasSingle2xxJSONResponse,
	"hasEmpty2xxResponse": 			hasEmpty2xxResponse,
	"hasValidRequestAndResponse": 	hasValidRequestAndResponse,
}