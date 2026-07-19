// Client side blob save, used for pem downloads
export function downloadBlob(content: string, filename: string, type = 'application/x-pem-file') {
	const blob = new Blob([content], { type });
	const url = URL.createObjectURL(blob);
	const a = document.createElement('a');
	a.href = url;
	a.download = filename;
	a.click();
	URL.revokeObjectURL(url);
}
