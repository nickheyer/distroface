export type TemplateCategory = 'Continuous integration' | 'Deployment' | 'Automation';

export const templateCategories: TemplateCategory[] = [
	'Continuous integration',
	'Deployment',
	'Automation'
];

export const workflowDir = '.distroface/workflows';

export interface WorkflowTemplate {
	id: string;
	name: string;
	description: string;
	category: TemplateCategory;
	fileName: string;
	// Enry language names that suggest this template
	languages: string[];
	// Root entries that suggest this template
	files: RegExp[];
	mark: string;
	color: string;
	content: string;
}

const onBranch = `on:
  push:
    branches: [ "__BRANCH__" ]
  pull_request:
    branches: [ "__BRANCH__" ]`;

export const workflowTemplates: WorkflowTemplate[] = [
	{
		id: 'blank',
		name: 'Simple workflow',
		description:
			'Start with a file that runs a couple of shell commands on push, pull request, and by hand.',
		category: 'Automation',
		fileName: 'blank.yml',
		languages: [],
		files: [],
		mark: 'CI',
		color: '#6e7781',
		content: `# A basic workflow to help you get started with Actions

name: CI

# Runs on pushes and pull requests to the default branch, and by hand
${onBranch}
  workflow_dispatch:

# A run is made of one or more jobs, they run in parallel unless they need each other
jobs:
  build:
    # The runner label this job needs
    runs-on: ubuntu-latest

    steps:
      # Checks out the repository under $GITHUB_WORKSPACE
      - uses: actions/checkout@v4

      - name: Run a one-line script
        run: echo Hello, world!

      - name: Run a multi-line script
        run: |
          echo Add other steps to build,
          echo test, and deploy your project.
`
	},
	{
		id: 'go',
		name: 'Go',
		description: 'Build and test a Go module with the official Go image.',
		category: 'Continuous integration',
		fileName: 'go.yml',
		languages: ['Go'],
		files: [/^go\.mod$/],
		mark: 'Go',
		color: '#00ADD8',
		content: `# Builds and tests a Go module inside the official Go image

name: Go

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    container:
      image: golang:1.26
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: go build -v ./...

      - name: Test
        run: go test -v ./...
`
	},
	{
		id: 'node',
		name: 'Node.js',
		description:
			'Install dependencies, build, and test a Node.js project across supported versions.',
		category: 'Continuous integration',
		fileName: 'node.js.yml',
		languages: ['JavaScript', 'TypeScript'],
		files: [/^package\.json$/],
		mark: 'JS',
		color: '#339933',
		content: `# Installs, builds, and tests a Node.js project on each listed version

name: Node.js CI

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node-version: [20, 22]
    container:
      image: node:\${{ matrix.node-version }}
    steps:
      - uses: actions/checkout@v4

      - name: Install dependencies
        run: npm ci

      - name: Build
        run: npm run build --if-present

      - name: Test
        run: npm test
`
	},
	{
		id: 'python',
		name: 'Python application',
		description: 'Install dependencies, lint with flake8, and test with pytest.',
		category: 'Continuous integration',
		fileName: 'python-app.yml',
		languages: ['Python'],
		files: [/^(requirements\.txt|pyproject\.toml|setup\.py|Pipfile)$/],
		mark: 'Py',
		color: '#3572A5',
		content: `# Installs dependencies, lints, and tests a Python application

name: Python application

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    container:
      image: python:3.12
    steps:
      - uses: actions/checkout@v4

      - name: Install dependencies
        run: |
          python -m pip install --upgrade pip
          pip install flake8 pytest
          [ ! -f requirements.txt ] || pip install -r requirements.txt

      - name: Lint with flake8
        run: |
          flake8 . --count --select=E9,F63,F7,F82 --show-source --statistics
          flake8 . --count --exit-zero --max-complexity=10 --max-line-length=127 --statistics

      - name: Test with pytest
        run: pytest
`
	},
	{
		id: 'rust',
		name: 'Rust',
		description: 'Build and test a Cargo project with the official Rust image.',
		category: 'Continuous integration',
		fileName: 'rust.yml',
		languages: ['Rust'],
		files: [/^Cargo\.toml$/],
		mark: 'Rs',
		color: '#B7410E',
		content: `# Builds and tests a Cargo project

name: Rust

${onBranch}

env:
  CARGO_TERM_COLOR: always

jobs:
  build:
    runs-on: ubuntu-latest
    container:
      image: rust:1
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: cargo build --verbose

      - name: Run tests
        run: cargo test --verbose
`
	},
	{
		id: 'maven',
		name: 'Java with Maven',
		description: 'Build and test a Java project with Maven.',
		category: 'Continuous integration',
		fileName: 'maven.yml',
		languages: ['Java', 'Kotlin'],
		files: [/^pom\.xml$/],
		mark: 'Jv',
		color: '#B07219',
		content: `# Builds and tests a Java project with Maven

name: Java CI with Maven

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    container:
      image: maven:3-eclipse-temurin-21
    steps:
      - uses: actions/checkout@v4

      - name: Build with Maven
        run: mvn -B package --file pom.xml
`
	},
	{
		id: 'gradle',
		name: 'Java with Gradle',
		description: 'Build and test a Java project with Gradle.',
		category: 'Continuous integration',
		fileName: 'gradle.yml',
		languages: ['Java', 'Kotlin'],
		files: [/^(build|settings)\.gradle(\.kts)?$/],
		mark: 'Gr',
		color: '#02303A',
		content: `# Builds and tests a Java project with Gradle

name: Java CI with Gradle

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    container:
      image: gradle:8-jdk21
    steps:
      - uses: actions/checkout@v4

      - name: Build with Gradle
        run: gradle build --no-daemon
`
	},
	{
		id: 'dotnet',
		name: '.NET',
		description: 'Restore, build, and test a .NET project with the SDK image.',
		category: 'Continuous integration',
		fileName: 'dotnet.yml',
		languages: ['C#', 'F#'],
		files: [/\.(csproj|fsproj|sln)$/],
		mark: '.N',
		color: '#512BD4',
		content: `# Restores, builds, and tests a .NET project

name: .NET

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    container:
      image: mcr.microsoft.com/dotnet/sdk:9.0
    steps:
      - uses: actions/checkout@v4

      - name: Restore dependencies
        run: dotnet restore

      - name: Build
        run: dotnet build --no-restore

      - name: Test
        run: dotnet test --no-build --verbosity normal
`
	},
	{
		id: 'ruby',
		name: 'Ruby',
		description: 'Install gems with Bundler and run the test suite.',
		category: 'Continuous integration',
		fileName: 'ruby.yml',
		languages: ['Ruby'],
		files: [/^Gemfile$/],
		mark: 'Rb',
		color: '#701516',
		content: `# Installs gems and runs the test suite

name: Ruby

${onBranch}

jobs:
  test:
    runs-on: ubuntu-latest
    container:
      image: ruby:3.3
    steps:
      - uses: actions/checkout@v4

      - name: Install dependencies
        run: bundle install

      - name: Run tests
        run: bundle exec rake
`
	},
	{
		id: 'makefile',
		name: 'Makefile CI',
		description: 'Run make and make test on a plain runner image.',
		category: 'Continuous integration',
		fileName: 'makefile.yml',
		languages: ['Makefile'],
		files: [/^(GNUm|m|M)akefile$/],
		mark: 'Mk',
		color: '#427819',
		content: `# Runs the default make target, then the tests

name: Makefile CI

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: make

      - name: Run tests
        run: make test
`
	},
	{
		id: 'docker-image',
		name: 'Docker image',
		description:
			'Build the Dockerfile on every push and pull request with the runner BuildKit, no socket and no privileged runner.',
		category: 'Continuous integration',
		fileName: 'docker-image.yml',
		languages: ['Dockerfile'],
		files: [/^Dockerfile$/, /\.Dockerfile$/],
		mark: 'Dk',
		color: '#384D54',
		content: `# Builds the Dockerfile on every push and pull request. The runner provides
# BuildKit, so the job never touches the docker socket and any runner works.

name: Docker Image CI

${onBranch}

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build the Docker image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile
          push: false
          tags: \${{ github.repository }}:ci
`
	},
	{
		id: 'docker-publish',
		name: 'Publish Docker image',
		description:
			'Build on every version tag and push to this instance as namespace/name:tag with the job token. The run lists the image and the release pins it.',
		category: 'Deployment',
		fileName: 'docker-publish.yml',
		languages: ['Dockerfile'],
		files: [/^Dockerfile$/],
		mark: 'Dk',
		color: '#2496ED',
		content: `# Builds the Dockerfile on every version tag and pushes it to this
# instance's registry under the repository's own namespace and name.
# The job token already authenticates to the registry, no secrets needed.
# The pushed tags show up on the run page and are pinned on the release.

name: Publish Docker image

on:
  push:
    tags: [ "v*" ]
  workflow_dispatch:

jobs:
  push:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Image tags and labels
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: \${{ env.DISTROFACE_REGISTRY }}/\${{ github.repository }}
          tags: |
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
            type=raw,value=latest,enable={{is_default_branch}}
            type=sha

      - name: Build and push
        uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          tags: \${{ steps.meta.outputs.tags }}
          labels: \${{ steps.meta.outputs.labels }}
`
	},
	{
		id: 'docker-multiarch',
		name: 'Multi-arch Docker image',
		description:
			'Build for amd64 and arm64 on every version tag and push a manifest list to this instance. Emulation needs binfmt on the runner host.',
		category: 'Deployment',
		fileName: 'docker-multiarch.yml',
		languages: ['Dockerfile'],
		files: [/^Dockerfile$/],
		mark: 'Dk',
		color: '#0DB7ED',
		content: `# Builds the Dockerfile for two platforms and pushes one manifest list.
# Cross platform layers run under emulation, install binfmt on the runner
# host (for example docker run --privileged --rm tonistiigi/binfmt --install all).

name: Publish multi-arch image

on:
  push:
    tags: [ "v*" ]
  workflow_dispatch:

jobs:
  push:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build and push
        uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            \${{ env.DISTROFACE_REGISTRY }}/\${{ github.repository }}:\${{ github.ref_name }}
            \${{ env.DISTROFACE_REGISTRY }}/\${{ github.repository }}:latest
`
	},
	{
		id: 'release-build',
		name: 'Release build',
		description: 'Build on every version tag and keep the output as a downloadable run artifact.',
		category: 'Deployment',
		fileName: 'release.yml',
		languages: [],
		files: [],
		mark: 'Rl',
		color: '#8250DF',
		content: `# Builds on every version tag and keeps the output as a run artifact

name: Release build

on:
  push:
    tags: [ "v*" ]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Build
        run: |
          mkdir -p dist
          tar -czf dist/source-\${{ github.ref_name }}.tar.gz --exclude=dist --exclude=.git .

      - name: Upload build output
        uses: actions/upload-artifact@v4
        with:
          name: release-\${{ github.ref_name }}
          path: dist/
`
	},
	{
		id: 'manual',
		name: 'Manual workflow',
		description: 'A workflow started from the Actions tab with a typed input.',
		category: 'Automation',
		fileName: 'manual.yml',
		languages: [],
		files: [],
		mark: 'Mn',
		color: '#BF8700',
		content: `# A workflow that only runs when started by hand

name: Manual workflow

# Runs from the Actions tab or dfcli, the inputs show up as a form
on:
  workflow_dispatch:
    inputs:
      name:
        description: 'Person to greet'
        default: 'World'
        required: true
        type: string

jobs:
  greet:
    runs-on: ubuntu-latest
    steps:
      - name: Send greeting
        run: echo "Hello \${{ inputs.name }}"
`
	},
	{
		id: 'schedule',
		name: 'Scheduled job',
		description: 'Run a job every night on a cron schedule, with a manual trigger for testing.',
		category: 'Automation',
		fileName: 'nightly.yml',
		languages: [],
		files: [],
		mark: 'Cr',
		color: '#1F883D',
		content: `# Runs every night at 03:00 UTC and can be started by hand

name: Nightly

on:
  schedule:
    - cron: "0 3 * * *"
  workflow_dispatch:

jobs:
  nightly:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Report
        run: echo "Nightly run on $(date -u)"
`
	}
];

export const blankTemplate = workflowTemplates[0];

export function findTemplate(id: string): WorkflowTemplate | undefined {
	return workflowTemplates.find((t) => t.id === id);
}

export function renderTemplate(t: WorkflowTemplate, branch: string): string {
	return t.content.replaceAll('__BRANCH__', branch || 'main');
}

// Languages rank by bytes, root files confirm a toolchain
export function suggestTemplates(
	languages: string[],
	rootFiles: string[],
	max = 4
): WorkflowTemplate[] {
	const scored: Array<{ t: WorkflowTemplate; score: number }> = [];
	for (const t of workflowTemplates) {
		let score = 0;
		const rank = Math.min(
			...t.languages.map((l) => {
				const i = languages.indexOf(l);
				return i < 0 ? Infinity : i;
			})
		);
		if (rank !== Infinity) score += 10 - Math.min(rank, 5);
		if (t.files.some((re) => rootFiles.some((f) => re.test(f)))) score += 8;
		if (score > 0) scored.push({ t, score });
	}
	return scored
		.sort((a, b) => b.score - a.score)
		.slice(0, max)
		.map((s) => s.t);
}
