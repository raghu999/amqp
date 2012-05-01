package main

import (
  "encoding/xml"
  "errors"
  "fmt"
  "io/ioutil"
  "log"
  "os"
  "regexp"
  "strings"
  "bytes"
  "text/template"
)

var (
  ErrUnknownType   = errors.New("Unknown field type in gen")
  ErrUnknownDomain = errors.New("Unknown domain type in gen")
)

var amqpTypeToNative = map[string]string{
  "bit":        "bool",
  "octet":      "byte",
  "shortshort": "uint8",
  "short":      "uint16",
  "long":       "uint32",
  "longlong":   "uint64",
  "timestamp":  "time.Time",
  "table":      "Table",
  "shortstr":   "string",
  "longstr":    "string",
}

type Rule struct {
  Name string   `xml:"name,attr"`
  Docs []string `xml:"doc"`
}

type Doc struct {
  Type string `xml:"type,attr"`
  Body string `xml:",innerxml"`
}

type Chassis struct {
  Name      string `xml:"name,attr"`
  Implement string `xml:"implement,attr"`
}

type Assert struct {
  Check  string `xml:"check,attr"`
  Value  string `xml:"value,attr"`
  Method string `xml:"method,attr"`
}

type Field struct {
  Name     string   `xml:"name,attr"`
  Domain   string   `xml:"domain,attr"`
  Type     string   `xml:"type,attr"`
  Label    string   `xml:"label,attr"`
  Reserved bool     `xml:"reserved,attr"`
  Docs     []Doc    `xml:"doc"`
  Asserts  []Assert `xml:"assert"`
}

type Method struct {
  Name        string    `xml:"name,attr"`
  Response    string    `xml:"response>name,attr"`
  Synchronous bool      `xml:"synchronous,attr"`
  Content     bool      `xml:"content,attr"`
  Index       string    `xml:"index,attr"`
  Label       string    `xml:"label,attr"`
  Docs        []Doc     `xml:"doc"`
  Rules       []Rule    `xml:"rule"`
  Fields      []Field   `xml:"field"`
  Chassis     []Chassis `xml:"chassis"`
}

type Class struct {
  Name    string    `xml:"name,attr"`
  Handler string    `xml:"handler,attr"`
  Index   string    `xml:"index,attr"`
  Label   string    `xml:"label,attr"`
  Docs    []Doc     `xml:"doc"`
  Methods []Method  `xml:"method"`
  Chassis []Chassis `xml:"chassis"`
}

type Domain struct {
  Name  string `xml:"name,attr"`
  Type  string `xml:"type,attr"`
  Label string `xml:"label,attr"`
  Rules []Rule `xml:"rule"`
  Docs  []Doc  `xml:"doc"`
}

type Constant struct {
  Name  string `xml:"name,attr"`
  Value int    `xml:"value,attr"`
  Doc   []Doc  `xml:"doc"`
}

type Amqp struct {
  Major   int    `xml:"major,attr"`
  Minor   int    `xml:"minor,attr"`
  Port    int    `xml:"port,attr"`
  Comment string `xml:"comment,attr"`

  Constants []Constant `xml:"constant"`
  Domains   []Domain   `xml:"domain"`
  Classes   []Class    `xml:"class"`
}

type renderer struct {
  Root       Amqp
  bitcounter int
}

type fieldset struct {
  AmqpType string
  NativeType string
  Fields []Field
  *renderer
}

var (
  helpers = template.FuncMap{
    "camel": camel,
    "clean": clean,
  }

  packageTemplate = template.Must(template.New("package").Funcs(helpers).Parse(`
  /* GENERATED FILE - DO NOT EDIT */
  /* Rebuild from the protocol/gen.go tool */

  {{with .Root}}
  package amqp

  import (
    "fmt"
    "encoding/binary"
    "io"
  )

  const (
  {{range .Constants}}
  {{range .Doc}}
  /* {{.Body | clean}} */
  {{end}}{{.Name | camel}} = {{.Value}} {{end}}
  )

  {{range .Classes}}
    {{$class := .}}
    {{range .Methods}}
      {{$method := .}}
			{{$struct := camel $class.Name $method.Name}}
      {{if .Docs}}/* {{range .Docs}} {{.Body | clean}} {{end}} */{{end}}
      type {{$struct}} struct {
        {{range .Fields}}
        {{$.FieldName .}} {{$.FieldType . | $.NativeType}} {{if .Label}}// {{.Label}}{{end}}{{end}}
				{{if .Content}}Properties Properties
				Body []byte{{end}}
      }

			func (me *{{$struct}}) id() (uint16, uint16) {
				return {{$class.Index}}, {{$method.Index}}
			}

			func (me *{{$struct}}) wait() (bool) {
				return {{.Synchronous}}{{if $.HasField "NoWait" .}} && !me.NoWait{{end}}
			}

			{{if .Content}}
      func (me *{{$struct}}) GetContent() (Properties, []byte) {
        return me.Properties, me.Body
      }

      func (me *{{$struct}}) SetContent(properties Properties, body []byte) {
        me.Properties, me.Body = properties, body
      }
			{{end}}
      func (me *{{$struct}}) write(w io.Writer) (err error) {
        {{.Fields | $.Fieldsets | $.Partial "enc-"}}
        return
      }

      func (me *{{$struct}}) read(r io.Reader) (err error) {
        {{.Fields | $.Fieldsets | $.Partial "dec-"}}
        return
      }
    {{end}}
  {{end}}

  func (me *Framer) parseMethodFrame(channel uint16, size uint32) (frame Frame, err error) {
    mf := &MethodFrame {
      ChannelId: channel,
    }

    if err = binary.Read(me.r, binary.BigEndian, &mf.ClassId); err != nil {
      return
    }

    if err = binary.Read(me.r, binary.BigEndian, &mf.MethodId); err != nil {
      return
    }

    switch mf.ClassId {
    {{range .Classes}}
    {{$class := .}}
    case {{.Index}}: // {{.Name}}
      switch mf.MethodId {
      {{range .Methods}}
      case {{.Index}}: // {{$class.Name}} {{.Name}}
        //fmt.Println("NextMethod: class:{{$class.Index}} method:{{.Index}}")
        method := &{{camel $class.Name .Name}}{}
        if err = method.read(me.r); err != nil {
          return
        }
        mf.Method = method
      {{end}}
      default:
        return nil, fmt.Errorf("Bad method frame, unknown method %d for class %d", mf.MethodId, mf.ClassId)
      }
    {{end}}
    default:
      return nil, fmt.Errorf("Bad method frame, unknown class %d", mf.ClassId)
    }

    return mf, nil
  }
  {{end}}

  {{define "enc-bit"}}
    var bits byte
    {{range $off, $field := .Fields}}
    if me.{{$field | $.FieldName}} { bits |= 1 << {{$off}} }
    {{end}}
    if err = binary.Write(w, binary.BigEndian, bits); err != nil { return }
  {{end}}
  {{define "enc-octet"}}
    {{range .Fields}} if err = binary.Write(w, binary.BigEndian, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-shortshort"}}
    {{range .Fields}} if err = binary.Write(w, binary.BigEndian, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-short"}}
    {{range .Fields}} if err = binary.Write(w, binary.BigEndian, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-long"}}
    {{range .Fields}} if err = binary.Write(w, binary.BigEndian, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-longlong"}}
    {{range .Fields}} if err = binary.Write(w, binary.BigEndian, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-timestamp"}}
    {{range .Fields}} if err = writeTimestamp(w, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-shortstr"}}
    {{range .Fields}} if err = writeShortstr(w, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-longstr"}}
    {{range .Fields}} if err = writeLongstr(w, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "enc-table"}}
    {{range .Fields}} if err = writeTable(w, me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}

  {{define "dec-bit"}}
    var bits byte
    if err = binary.Read(r, binary.BigEndian, &bits); err != nil {
      return
    }
    {{range $off, $field := .Fields}} me.{{$field | $.FieldName}} = (bits & (1 << {{$off}}) > 0)
    {{end}}
  {{end}}
  {{define "dec-octet"}}
    {{range .Fields}} if err = binary.Read(r, binary.BigEndian, &me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-shortshort"}}
    {{range .Fields}} if err = binary.Read(r, binary.BigEndian, &me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-short"}}
    {{range .Fields}} if err = binary.Read(r, binary.BigEndian, &me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-long"}}
    {{range .Fields}} if err = binary.Read(r, binary.BigEndian, &me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-longlong"}}
    {{range .Fields}} if err = binary.Read(r, binary.BigEndian, &me.{{. | $.FieldName}}); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-timestamp"}}
    {{range .Fields}} if me.{{. | $.FieldName}}, err = readTimestamp(r); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-shortstr"}}
    {{range .Fields}} if me.{{. | $.FieldName}}, err = readShortstr(r); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-longstr"}}
    {{range .Fields}} if me.{{. | $.FieldName}}, err = readLongstr(r); err != nil { return }
    {{end}}
  {{end}}
  {{define "dec-table"}}
    {{range .Fields}} if me.{{. | $.FieldName}}, err = readTable(r); err != nil { return }
    {{end}}
  {{end}}

  `))
)

func (me *renderer) Partial(prefix string, fields []fieldset) (s string, err error) {
  var buf bytes.Buffer
  for _, set := range fields {
    name := prefix + set.AmqpType
    t := packageTemplate.Lookup(name)
    if t == nil {
      return "", errors.New(fmt.Sprintf("Missing template: %s", name))
    }
    if err = t.Execute(&buf, set); err != nil {
      return
    }
  }
  return string(buf.Bytes()), nil
}

// Groups the fields so that the right encoder/decoder can be called
func (me *renderer) Fieldsets(fields []Field) (f []fieldset, err error) {
  var tmp []fieldset

  for _, field := range fields {
    cur := fieldset{}
    cur.AmqpType, err = me.FieldType(field)
    if err != nil {
      return
    }

    cur.NativeType, err = me.NativeType(cur.AmqpType)
    if err != nil {
      return
    }

    cur.Fields = append(cur.Fields, field)
    tmp = append(tmp, cur)
  }

  if len(tmp) > 0 {
    acc := tmp[0]
    for i, cur := range tmp[1:] {
      if acc.AmqpType == cur.AmqpType {
        acc.Fields = append(acc.Fields, cur.Fields...)
        if i == len(tmp) {
          f = append(f, acc)
        }
      } else {
        f = append(f, acc)
        acc = cur
      }
    }
  }

  return
}

func (me *renderer) HasField(field string, method Method) bool {
	for _, f := range method.Fields {
		name := me.FieldName(f)
		if name == field {
			return true
		}
	}
	return false
}

func (me *renderer) FieldEncode(field Field) (str string, err error) {
  var fieldType, nativeType, fieldName string

  if fieldType, err = me.FieldType(field); err != nil {
    return "", err
  }

  if nativeType, err = me.NativeType(fieldType); err != nil {
    return "", err
  }

  if field.Reserved {
    fieldName = camel(field.Name)
    str += fmt.Sprintf("var %s %s\n", fieldName, nativeType)
  } else {
    fieldName = fmt.Sprintf("me.%s", camel(field.Name))
  }

  if fieldType == "bit" {
    if me.bitcounter == 0 {
      str += fmt.Sprintf("buf.PutOctet(0)\n")
    }
    str += fmt.Sprintf("buf.Put%s(%s, %d)", camel(fieldType), fieldName, me.bitcounter)
    me.bitcounter = me.bitcounter + 1
    return
  }

  me.bitcounter = 0
  str += fmt.Sprintf("buf.Put%s(%s)", camel(fieldType), fieldName)

  return
}

func (me *renderer) FinishDecode() (string, error) {
  if me.bitcounter > 0 {
    me.bitcounter = 0
    // The last field in the fieldset was a bit field
    // which means we need to consume this word.  This would
    // be better done with object scoping
    return "me.NextOctet()", nil
  }
  return "", nil
}

func (me *renderer) FieldDecode(name string, field Field) (string, error) {
  var str string

  t, err := me.FieldType(field)
  if err != nil {
    return "", err
  }

  if field.Reserved {
    str = "_ = "
  } else {
    str = fmt.Sprintf("%s.%s = ", name, camel(field.Name))
  }

  if t == "bit" {
    str += fmt.Sprintf("me.Next%s(%d)", camel(t), me.bitcounter)
    me.bitcounter = me.bitcounter + 1
    return str, nil
  }

  if me.bitcounter > 0 {
    // We've advanced past a bit word, so consume it before the real decoding
    str = "me.NextOctet() // reset\n" + str
    me.bitcounter = 0
  }

  return str + fmt.Sprintf("me.Next%s()", camel(t)), nil

}

func (me *renderer) Domain(field Field) (domain Domain, err error) {
  for _, domain = range me.Root.Domains {
    if field.Domain == domain.Name {
      return
    }
  }
  return domain, nil
  //return domain, ErrUnknownDomain
}

func (me *renderer) FieldName(field Field) (t string) {
  t = camel(field.Name)

  if field.Reserved {
    t = strings.ToLower(t)
  }

  return
}

func (me *renderer) FieldType(field Field) (t string, err error) {
  t = field.Type

  if t == "" {
    var domain Domain
    domain, err = me.Domain(field)
    if err != nil {
      return "", err
    }
    t = domain.Type
  }

  return
}

func (me *renderer) NativeType(amqpType string) (t string, err error) {
  if t, ok := amqpTypeToNative[amqpType]; ok {
    return t, nil
  }
  return "", ErrUnknownType
}

func (me *renderer) Tag(d Domain) string {
  label := "`"

  label += `domain:"` + d.Name + `"`

  if len(d.Type) > 0 {
    label += `,type:"` + d.Type + `"`
  }

  label += "`"

  return label
}

func clean(body string) (res string) {
  return strings.Replace(body, "\r", "", -1)
}

func camel(parts ...string) (res string) {
  for _, in := range parts {
    delim := regexp.MustCompile(`^\w|[-_]\w`)

    res += delim.ReplaceAllStringFunc(in, func(match string) string {
      switch len(match) {
      case 1:
        return strings.ToUpper(match)
      case 2:
        return strings.ToUpper(match[1:])
      }
      panic("unreachable")
    })
  }

  return
}

func main() {
  var r renderer

  spec, err := ioutil.ReadAll(os.Stdin)
  if err != nil {
    log.Fatalln("Please pass spec on stdin", err)
  }

  err = xml.Unmarshal(spec, &r.Root)

  if err != nil {
    log.Fatalln("Could not parse XML:", err)
  }

  if err = packageTemplate.Execute(os.Stdout, &r); err != nil {
    log.Fatalln("Generate error: ", err)
  }
}
