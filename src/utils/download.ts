/** Скачивает base64-строку как файл через `<a download>` (2026-08-24) — общий
 * хелпер, вынесенный из sbe-lims (downloadDocx для протокола) — тот же код
 * нужен для Excel-экспорта в sbe-requests. Бросает исключение при сбое (напр.
 * невалидный base64) — Notice/обработка остаются на вызывающей стороне, чтобы
 * этот модуль не тянул зависимость на obsidian. */
export function downloadBase64File(base64Data: string, fileName: string, mimeType: string): void {
  const bin = atob(base64Data);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  const blob = new Blob([bytes], { type: mimeType });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = fileName;
  a.click();
  URL.revokeObjectURL(a.href);
}
