import { useEffect, useMemo, useState } from 'react';
import { api } from '../lib/api';
import type { ConsultationResult } from '../types';
import { t, type Language } from '../i18n/translations';

type Props = {
  traderId?: string;
  language: Language;
  autoModeEnabled?: boolean;
  autoModeLoading?: boolean;
  autoModeUpdating?: boolean;
  onToggleAutoMode?: () => void;
};

const parseSymbolsInput = (input: string): string[] => {
  return input
    .split(/[\s,;]+/)
    .map((item) => item.trim())
    .filter(Boolean);
};

const formatNumber = (value?: number, digits = 2) => {
  if (value === undefined || value === null || Number.isNaN(value)) {
    return '--';
  }
  return value.toFixed(digits);
};

const formatActionLabel = (action?: string, language?: Language) => {
  if (!action) return 'WAIT';
  switch (action) {
    case 'open_long':
      return `${t('long', language ?? 'en')} ✅`;
    case 'open_short':
      return `${t('short', language ?? 'en')} 🔻`;
    case 'close_long':
      return `${t('long', language ?? 'en')} ✖`;
    case 'close_short':
      return `${t('short', language ?? 'en')} ✖`;
    default:
      return action.toUpperCase();
  }
};

const clampLeverage = (input: string): number => {
  const numberValue = Number(input);
  if (!Number.isFinite(numberValue) || numberValue <= 0) {
    return 5;
  }
  return Math.max(1, Math.min(125, Math.round(numberValue)));
};

const clampBalance = (input: string): number => {
  const numberValue = Number(input);
  if (!Number.isFinite(numberValue) || numberValue <= 0) {
    return 100;
  }
  return Math.max(100, Math.round(numberValue));
};

export function ConsultationPage({
  traderId,
  language,
  autoModeEnabled,
  autoModeLoading,
  autoModeUpdating,
  onToggleAutoMode,
}: Props) {
  const [symbolsInput, setSymbolsInput] = useState('');
  const [leverageInput, setLeverageInput] = useState('5');
  const [balanceInput, setBalanceInput] = useState('1000');
  const [settingsUpdatedAt, setSettingsUpdatedAt] = useState<string>();
  const [isLoadingSettings, setIsLoadingSettings] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isRequesting, setIsRequesting] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [result, setResult] = useState<ConsultationResult | null>(null);
  const [showCot, setShowCot] = useState(false);

  useEffect(() => {
    if (!traderId) {
      setSymbolsInput('');
      setResult(null);
      setSettingsUpdatedAt(undefined);
      return;
    }
    let isMounted = true;
    setIsLoadingSettings(true);
    setErrorMessage(null);
    setResult(null);
    setShowCot(false);

    api
      .getConsultationSettings(traderId)
      .then((data) => {
        if (!isMounted) return;
        const nextSymbols = (data.symbols || []).join('\n');
        setSymbolsInput(nextSymbols);
        setLeverageInput(String(data.leverage || 5));
        setBalanceInput(String(data.balance || 1000));
        setSettingsUpdatedAt(data.updated_at);
      })
      .catch((err) => {
        if (!isMounted) return;
        setErrorMessage(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (isMounted) {
          setIsLoadingSettings(false);
        }
      });

    return () => {
      isMounted = false;
    };
  }, [traderId]);

  const parsedSymbols = useMemo(() => parseSymbolsInput(symbolsInput), [symbolsInput]);

  const handleSave = async () => {
    if (!traderId) return;
    const nextSymbols = parsedSymbols;
    if (nextSymbols.length === 0) {
      setErrorMessage(t('consultErrorNoSymbols', language));
      return;
    }
    setIsSaving(true);
    setErrorMessage(null);
    try {
      const sanitizedLeverage = clampLeverage(leverageInput);
      const sanitizedBalance = clampBalance(balanceInput);
      const payload = {
        trader_id: traderId,
        symbols: nextSymbols,
        leverage: sanitizedLeverage,
        balance: sanitizedBalance,
      };
      const saved = await api.saveConsultationSettings(payload);
      setSymbolsInput((saved.symbols || []).join('\n'));
      setLeverageInput(String(saved.leverage ?? sanitizedLeverage));
      setBalanceInput(String(saved.balance ?? sanitizedBalance));
      setSettingsUpdatedAt(saved.updated_at);
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : String(err));
    } finally {
      setIsSaving(false);
    }
  };

  const handleRequest = async () => {
    if (!traderId) return;
    const nextSymbols = parsedSymbols;
    if (nextSymbols.length === 0) {
      setErrorMessage(t('consultErrorNoSymbols', language));
      return;
    }
    setIsRequesting(true);
    setErrorMessage(null);
    setShowCot(false);
    try {
      const sanitizedLeverage = clampLeverage(leverageInput);
      const sanitizedBalance = clampBalance(balanceInput);
      const payload = {
        trader_id: traderId,
        symbols: nextSymbols,
        leverage: sanitizedLeverage,
        balance: sanitizedBalance,
      };
      const advice = await api.requestConsultation(payload);
      setResult(advice);
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : String(err));
    } finally {
      setIsRequesting(false);
    }
  };

  if (!traderId) {
    return (
      <div className="binance-card p-6 text-center" style={{ color: '#848E9C' }}>
        {t('consultNeedTrader', language)}
      </div>
    );
  }

  return (
    <div className="space-y-6 animate-fade-in">
      <div className="binance-card p-6 flex flex-col lg:flex-row gap-4 lg:items-center">
        <div>
          <h2 className="text-xl font-bold mb-1" style={{ color: '#EAECEF' }}>
            {t('consultationMode', language)}
          </h2>
          <p className="text-sm" style={{ color: '#848E9C' }}>
            {t('consultDescription', language)}
          </p>
        </div>
        <div className="flex-1" />
        <div className="rounded-lg px-4 py-3 text-sm" style={{ background: 'rgba(240,185,11,0.08)', color: '#F0B90B', border: '1px solid rgba(240, 185, 11, 0.25)' }}>
          ⚠️ {t('consultation', language)} · {t('consultRequest', language)}
        </div>
      </div>

      <div className="binance-card p-5 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
        <div>
          <p className="text-sm font-semibold" style={{ color: '#EAECEF' }}>
            {autoModeEnabled ? t('autoModeOn', language) : t('autoModeOff', language)}
          </p>
          <p className="text-xs" style={{ color: '#848E9C' }}>
            {t('autoModeHint', language)}
          </p>
        </div>
        <button
          onClick={onToggleAutoMode}
          disabled={!onToggleAutoMode || autoModeLoading || autoModeUpdating}
          className="px-4 py-2 rounded font-semibold text-sm border"
          style={
            autoModeEnabled
              ? { borderColor: '#0ECB81', color: '#0ECB81' }
              : { borderColor: '#F0B90B', color: '#F0B90B' }
          }
        >
          {autoModeEnabled ? t('autoModeDisable', language) : t('autoModeEnable', language)}
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
        <div className="lg:col-span-2 binance-card p-5 space-y-3">
          <label className="font-semibold text-sm" style={{ color: '#EAECEF' }}>
            {t('consultSymbols', language)}
          </label>
          <textarea
            value={symbolsInput}
            disabled={isLoadingSettings}
            onChange={(e) => setSymbolsInput(e.target.value)}
            rows={6}
            placeholder={t('consultSymbolsPlaceholder', language)}
            className="w-full rounded px-4 py-3 text-sm resize-none focus:outline-none"
            style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
          />
          <p className="text-xs" style={{ color: '#848E9C' }}>
            {t('consultSymbolsHint', language)}
          </p>
          {settingsUpdatedAt && (
            <p className="text-xs" style={{ color: '#5E6673' }}>
              {t('consultLastSaved', language)}: {new Date(settingsUpdatedAt).toLocaleString()}
            </p>
          )}
        </div>

        <div className="space-y-4">
          <div className="binance-card p-5 space-y-3">
            <label className="font-semibold text-sm" style={{ color: '#EAECEF' }}>
              {t('consultLeverage', language)}
            </label>
            <input
              type="text"
              inputMode="numeric"
              value={leverageInput}
              onChange={(e) => setLeverageInput(e.target.value)}
              className="w-full rounded px-4 py-3 text-sm focus:outline-none"
              style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
              placeholder="5"
            />
          </div>

          <div className="binance-card p-5 space-y-3">
            <label className="font-semibold text-sm" style={{ color: '#EAECEF' }}>
              {t('consultBalance', language)}
            </label>
            <input
              type="text"
              inputMode="decimal"
              value={balanceInput}
              onChange={(e) => setBalanceInput(e.target.value)}
              className="w-full rounded px-4 py-3 text-sm focus:outline-none"
              style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}
              placeholder="1000"
            />
          </div>

          <div className="binance-card p-5 space-y-3">
            <button
              onClick={handleSave}
              disabled={isSaving || isLoadingSettings || parsedSymbols.length === 0}
              className="w-full py-3 rounded font-semibold transition-all"
              style={{
                background: isSaving ? '#5E6673' : '#F0B90B',
                color: isSaving ? '#0B0E11' : '#000',
                opacity: isSaving ? 0.7 : 1,
              }}
            >
              {isSaving ? '...' : t('consultSave', language)}
            </button>
            <button
              onClick={handleRequest}
              disabled={isRequesting || parsedSymbols.length === 0}
              className="w-full py-3 rounded font-semibold transition-all border"
              style={{
                borderColor: '#F0B90B',
                color: '#F0B90B',
                opacity: isRequesting ? 0.6 : 1,
              }}
            >
              {isRequesting ? '⏳' : t('consultRequest', language)}
            </button>
          </div>
        </div>
      </div>

      {errorMessage && (
        <div className="binance-card p-4 text-sm" style={{ color: '#F6465D', border: '1px solid rgba(246, 70, 93, 0.4)' }}>
          {errorMessage}
        </div>
      )}

      <div className="binance-card p-6 space-y-4">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2">
          <div>
            <h3 className="text-lg font-bold" style={{ color: '#EAECEF' }}>
              {t('consultResultTitle', language)}
            </h3>
            {result && (
              <p className="text-xs" style={{ color: '#848E9C' }}>
                {t('consultAdviceTimestamp', language)}: {new Date(result.timestamp).toLocaleString()}
              </p>
            )}
          </div>
          {result && (
            <div className="flex flex-wrap gap-2 text-xs">
              <span className="px-2 py-1 rounded" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}>
                {result.symbols.join(', ')}
              </span>
              <span className="px-2 py-1 rounded" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}>
                {result.leverage}x
              </span>
              <span className="px-2 py-1 rounded" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#EAECEF' }}>
                {formatNumber(result.balance, 2)} USDT
              </span>
            </div>
          )}
        </div>

        {!result && (
          <p className="text-sm" style={{ color: '#5E6673' }}>
            {t('consultNoResult', language)}
          </p>
        )}

        {result && result.decisions && result.decisions.length > 0 && (
          <div className="space-y-3">
            <p className="text-xs" style={{ color: '#848E9C' }}>
              {t('consultDecisionsCount', language, { count: result.decisions.length })}
            </p>
            {result.decisions.map((decision, index) => (
              <div key={`${decision.symbol}-${decision.action}-${index}`} className="rounded-lg p-4" style={{ background: '#0B0E11', border: '1px solid #2B3139' }}>
                <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
                  <div>
                    <div className="text-sm font-semibold" style={{ color: '#EAECEF' }}>
                      {decision.symbol} · {formatActionLabel(decision.action, language)}
                    </div>
                    <div className="text-xs" style={{ color: '#848E9C' }}>
                      #{index + 1}
                    </div>
                  </div>
                  <div className="text-xs text-right space-y-1" style={{ color: '#848E9C' }}>
                    {decision.confidence !== undefined && <div>🎯 {decision.confidence}%</div>}
                    {decision.position_size_usd && <div>💰 {formatNumber(decision.position_size_usd, 0)} USDT</div>}
                  </div>
                </div>

                <div className="grid grid-cols-1 sm:grid-cols-3 gap-2 text-xs mt-3">
                  <div>
                    <span style={{ color: '#848E9C' }}>SL</span>: <span style={{ color: '#EAECEF' }}>{formatNumber(decision.stop_loss, 4)}</span>
                  </div>
                  <div>
                    <span style={{ color: '#848E9C' }}>TP</span>: <span style={{ color: '#EAECEF' }}>{formatNumber(decision.take_profit, 4)}</span>
                  </div>
                  <div>
                    <span style={{ color: '#848E9C' }}>Risk</span>: <span style={{ color: '#EAECEF' }}>{decision.risk_usd ? formatNumber(decision.risk_usd, 0) : '--'} USDT</span>
                  </div>
                </div>

                {decision.tp_targets && decision.tp_targets.length > 0 && (
                  <div className="mt-2 text-xs space-y-1">
                    <span style={{ color: '#848E9C' }}>TP Targets:</span>
                    <div className="flex flex-wrap gap-2">
                      {decision.tp_targets.map((tp, idx) => (
                        <span key={`${tp.price}-${idx}`} className="px-2 py-1 rounded" style={{ background: '#181A20', color: '#EAECEF', border: '1px solid #2B3139' }}>
                          {tp.price ? tp.price.toFixed(4) : '--'} · {tp.size_pct ? `${tp.size_pct}%` : tp.size_usd ? `${tp.size_usd} USDT` : '--'}
                        </span>
                      ))}
                    </div>
                  </div>
                )}

                {decision.reasoning && (
                  <p className="text-sm mt-3" style={{ color: '#C3C8D4' }}>
                    {decision.reasoning}
                  </p>
                )}
              </div>
            ))}
          </div>
        )}

        {result && result.cot_trace && (
          <div className="mt-4">
            <button
              onClick={() => setShowCot((prev) => !prev)}
              className="text-xs font-semibold flex items-center gap-2 hover:opacity-80 transition-all"
              style={{ color: '#F0B90B' }}
            >
              {showCot ? t('collapse', language) : t('expand', language)} · {t('consultCotTitle', language)}
            </button>
            {showCot && (
              <pre className="mt-2 p-4 rounded text-xs whitespace-pre-wrap overflow-auto" style={{ background: '#0B0E11', border: '1px solid #2B3139', color: '#C3C8D4' }}>
                {result.cot_trace}
              </pre>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
