/*
 * SPDX-License-Identifier: AGPL-3.0-or-later
 * Copyright 2021 Kopano and its licensors
 */

package newusers

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/libregraph/idm/pkg/ldappassword"
	"github.com/libregraph/idm/server/handler/ldif"
)

const userTemplateLDIF = `<<- /* */ ->>
dn: uid=<<.entry.Name>>,<<.BaseDN>>
objectClass: posixAccount
objectClass: top
objectClass: inetOrgPerson
objectClass: kopano-user
uid: <<.entry.Name>>
uidNumber: <<with .detail.uidNumber>><<.>><<else>><<AutoIncrement>><<end>>
<<- with .detail.gidNumber>>
gidNumber: <<.>>
<<- end>>
<<- with .detail.userPassword>>
userPassword: <<.>>
<<- end>>
mail: <<.entry.Name>>@{{.MailDomain}}
<<- range .detail.mail>>
kopanoAliases: <<.>>
<<- end>>
cn: <<.detail.cn>>
<<- with .detail.givenName>>
givenName: <<.>>
<<- end>>
<<- with .detail.sn>>
sn: <<.>>
<<- end>>
kopanoAccount: 1
kopanoAdmin: 0
`

func outputLDIF(r io.Reader) error {
	entries, err := parsePasswdFile(r)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	options := &ldif.Options{
		BaseDN:            "ou=users,ou={{.Company}},{{.BaseDN}}",
		DefaultMailDomain: "kopano.local",
	}
	out := os.Stdout

	var details []map[string]interface{}
	var autoIncrement int64 = 1000

	for _, entry := range entries {
		// Pre process entries, create details for each.
		detail := make(map[string]interface{})
		if entry.UID != "" {
			n, convErr := strconv.ParseInt(entry.UID, 10, 64)
			if convErr != nil {
				return fmt.Errorf("uid must be numeric, line %d", entry.line)
			}
			detail["uidNumber"] = n
			if n > autoIncrement {
				autoIncrement = n
			}
		}
		if entry.GID != "" {
			n, convErr := strconv.ParseInt(entry.GID, 10, 64)
			if convErr != nil {
				return fmt.Errorf("gid must be numeric, line %d", entry.line)
			}
			detail["gidNumber"] = n
			if n > autoIncrement {
				autoIncrement = n
			}
		}

		details = append(details, detail)
	}

	m := map[string]interface{}{}
	tpl, err := template.New("tpl").Delims("<<", ">>").Funcs(ldif.TemplateFuncs(m, options)).Parse(userTemplateLDIF)
	if err != nil {
		panic(err)
	}

	for idx, entry := range entries {
		detail := details[idx]
		m["entry"] = entry
		m["detail"] = detail

		if entry.Passwd != "" {
			hash, hashErr := ldappassword.Hash(entry.Passwd, DefaultPasswordScheme)
			if hashErr != nil {
				return hashErr
			}
			detail["userPassword"] = hash
		} else {
			// Use entry name as password, if none is set.
			detail["userPassword"] = entry.Name
		}
		if entry.Gecos != "" {
			var mail []string
			// full name,building,office phone,home phone,other...
			gecosParts := strings.Split(entry.Gecos, ",")
			fields := len(gecosParts)
			if fields > 0 {
				detail["cn"] = gecosParts[0]
				nameParts := strings.SplitN(gecosParts[0], " ", 2)
				if len(nameParts) == 2 {
					detail["givenName"] = nameParts[0]
					detail["sn"] = nameParts[1]
				}
			}
			if fields > 1 {
				// Find email addresses in rest of fields.
				for _, value := range gecosParts[1:] {
					if isValidEmail(value) {
						if options.DefaultMailDomain != "" && strings.HasSuffix(value, "@"+options.DefaultMailDomain) {
							value = strings.TrimSuffix(value, "@"+options.DefaultMailDomain) + "@{{.MailDomain}}"
						}
						mail = append(mail, value)
					}
				}
			}
			detail["mail"] = mail
		} else {
			detail["cn"] = entry.Name
		}
		if entry.UID == "" || entry.GID == "" {
			autoIncrement++
			if entry.UID == "" {
				detail["uidNumber"] = autoIncrement
			}
			if entry.GID == "" {
				detail["gidNumber"] = autoIncrement
			}
		}

		fmt.Fprintf(out, "# %d: %s\n", entry.line, strings.TrimSpace(entry.Name))
		if err = tpl.Execute(out, m); err != nil {
			return fmt.Errorf("# failed to generate: %w", err)
		}
		fmt.Fprintf(out, "\n")
	}

	return nil
}
