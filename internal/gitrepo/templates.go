package gitrepo

import (
	"fmt"
	"strings"
	"time"
)

// Gitignore templates offered at repository creation
var gitignoreTemplates = map[string]string{
	"Go":     "# Binaries\n*.exe\n*.dll\n*.so\n*.dylib\n*.test\n*.out\n\n# Dependency directories\nvendor/\n\n# Go workspace\ngo.work\ngo.work.sum\n\n# Environment\n.env\n",
	"Node":   "node_modules/\nnpm-debug.log*\nyarn-debug.log*\nyarn-error.log*\n.pnpm-debug.log*\ndist/\nbuild/\n.env\n.env.local\ncoverage/\n.cache/\n",
	"Python": "__pycache__/\n*.py[cod]\n*$py.class\n*.so\n.Python\nbuild/\ndist/\n*.egg-info/\n.venv/\nvenv/\nenv/\n.pytest_cache/\n.mypy_cache/\n.coverage\nhtmlcov/\n",
	"Rust":   "/target\nCargo.lock\n**/*.rs.bk\n*.pdb\n",
	"Java":   "*.class\n*.log\n*.jar\n*.war\n*.ear\ntarget/\nbuild/\n.gradle/\n.idea/\n*.iml\n",
	"C":      "*.o\n*.a\n*.so\n*.out\n*.exe\n*.obj\n*.dSYM/\nbuild/\n",
	"Ruby":   "*.gem\n*.rbc\n/.bundle/\n/vendor/bundle\n/log/\n/tmp/\n.byebug_history\n",
	"Svelte": "node_modules/\n.svelte-kit/\nbuild/\n.env\n.env.*\n!.env.example\nvite.config.js.timestamp-*\nvite.config.ts.timestamp-*\n",
	"Docker": "*.log\n.env\ndocker-compose.override.yml\n",
	"Global": ".DS_Store\nThumbs.db\n*.swp\n*.swo\n*~\n.idea/\n.vscode/\n",
}

// Names of the gitignore templates
func GitignoreTemplates() []string {
	out := make([]string, 0, len(gitignoreTemplates))
	for k := range gitignoreTemplates {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// Body of a gitignore template
func GitignoreTemplate(name string) (string, bool) {
	body, ok := gitignoreTemplates[name]
	return body, ok
}

// License identifiers offered at repository creation
func LicenseTemplates() []string {
	return []string{"MIT", "Apache-2.0", "BSD-3-Clause", "GPL-3.0", "Unlicense"}
}

// Body of a license with the holder and year filled in
func LicenseTemplate(id, holder string) (string, bool) {
	year := fmt.Sprint(time.Now().Year())
	switch id {
	case "MIT":
		return fmt.Sprintf(`MIT License

Copyright (c) %s %s

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`, year, holder), true
	case "Apache-2.0":
		return fmt.Sprintf(`Copyright %s %s

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
`, year, holder), true
	case "BSD-3-Clause":
		return fmt.Sprintf(`BSD 3-Clause License

Copyright (c) %s, %s

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
`, year, holder), true
	case "GPL-3.0":
		return fmt.Sprintf(`Copyright (C) %s %s

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
`, year, holder), true
	case "Unlicense":
		return `This is free and unencumbered software released into the public domain.

Anyone is free to copy, modify, publish, use, compile, sell, or
distribute this software, either in source code form or as a compiled
binary, for any purpose, commercial or non-commercial, and by any
means.

For more information, please refer to <https://unlicense.org>
`, true
	}
	return "", false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && strings.ToLower(s[j]) < strings.ToLower(s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
