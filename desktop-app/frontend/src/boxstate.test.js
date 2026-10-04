import { describe, it, expect } from 'vitest';
import { answersWithoutSTM } from './boxstate.js';

describe('answersWithoutSTM', () => {
  it('is false for a missing or offline record: nothing answers, that is the dead case', () => {
    expect(answersWithoutSTM(null)).toBe(false);
    expect(answersWithoutSTM(undefined)).toBe(false);
    expect(answersWithoutSTM({ kind: 'stock', offline: true })).toBe(false);
    expect(answersWithoutSTM({ kind: 'str', stmSilent: true, offline: true })).toBe(false);
  });

  it('is false for a live STM record whose agent answered', () => {
    expect(answersWithoutSTM({ kind: 'str', version: '0.9.74' })).toBe(false);
  });

  it('is true when the Bose firmware answered but the agent did not', () => {
    // This refresh only: stock :8090 answered, agent port silent.
    expect(answersWithoutSTM({ kind: 'str', version: '0.9.74', stmSilent: true })).toBe(true);
    // Discovery already degraded the record.
    expect(answersWithoutSTM({ kind: 'stock', stmNotRunning: true })).toBe(true);
    // STM was removed: a plain stock speaker.
    expect(answersWithoutSTM({ kind: 'stock' })).toBe(true);
  });
});
