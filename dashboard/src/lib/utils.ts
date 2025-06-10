import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function cleanLog(logs: { timestamp: string; message: string; sequence_num: number }[]): string[] {
  let combinedLogs = '';
  for (const log of logs) {
    combinedLogs += log.message
  }

  // Split combined logs into individual log entries
  const logEntries = combinedLogs.split('\n').filter(line => line.trim() !== '');

  const cleanedLogs = logEntries.map((entry) => {
    const message = entry;
    const cleanedLines = message.split('\n')
      .map(line => line.length > 8 ? line.substring(8) : '')
      .filter(line => line !== '');
    const cleanedMessage = cleanedLines.join('\n');
    return { message: cleanedMessage };
  });

  return cleanedLogs.map(log => log.message);
}